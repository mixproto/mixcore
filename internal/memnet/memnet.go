// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package memnet is an in-memory UDP network. Unit tests run drivers and
// simulators over it inside testing/synctest, where real sockets cannot be
// used (synctest cannot see goroutines blocked on network I/O). Delivery is
// like UDP: unordered guarantees are not modelled, but a full inbox drops the
// packet, and a packet to an address nobody listens on is lost.
package memnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"
)

const (
	inboxSize     = 512
	firstEphemera = 40000
	maxPacket     = 65507
)

// Errors returned by the network.
var (
	ErrAddrInUse = errors.New("memnet: address in use")
	ErrBadAddr   = errors.New("memnet: bad address")
)

var broadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})

// Network is one in-memory LAN.
type Network struct {
	mu       sync.Mutex
	conns    map[netip.AddrPort]*PacketConn
	nextPort uint16
	down     map[netip.Addr]bool
}

// New returns an empty network.
func New() *Network {
	return &Network{conns: map[netip.AddrPort]*PacketConn{}, nextPort: firstEphemera, down: map[netip.Addr]bool{}}
}

// ListenPacket binds addr ("10.0.0.2:10023"; port 0 picks a free port).
func (n *Network) ListenPacket(addr string) (*PacketConn, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadAddr, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if ap.Port() == 0 {
		for n.conns[netip.AddrPortFrom(ap.Addr(), n.nextPort)] != nil {
			n.nextPort++
		}
		ap = netip.AddrPortFrom(ap.Addr(), n.nextPort)
		n.nextPort++
	}
	if n.conns[ap] != nil {
		return nil, ErrAddrInUse
	}
	c := &PacketConn{net: n, local: ap, inbox: make(chan packet, inboxSize), closed: make(chan struct{}),
		deadlineChanged: make(chan struct{}, 1)}
	n.conns[ap] = c
	return c, nil
}

// Host returns a listen function for a host with the given IP. Its signature
// matches driver.ListenPacketFunc; the requested address is ignored except
// for its port.
func (n *Network) Host(ip string) func(ctx context.Context, network, address string) (net.PacketConn, error) {
	return func(_ context.Context, _, address string) (net.PacketConn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			port = "0"
		}
		return n.ListenPacket(net.JoinHostPort(ip, port))
	}
}

// SetDown cuts (true) or restores (false) every packet to and from ip.
func (n *Network) SetDown(ip string, down bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.down[netip.MustParseAddr(ip)] = down
}

func (n *Network) deliver(from netip.AddrPort, to netip.AddrPort, b []byte) {
	n.mu.Lock()
	var targets []*PacketConn
	if !n.down[from.Addr()] {
		if to.Addr() == broadcast {
			for ap, c := range n.conns {
				if ap.Port() == to.Port() && ap != from && !n.down[ap.Addr()] {
					targets = append(targets, c)
				}
			}
		} else if c := n.conns[to]; c != nil && !n.down[to.Addr()] {
			targets = append(targets, c)
		}
	}
	n.mu.Unlock()
	for _, c := range targets {
		c.enqueue(packet{from: from, data: slices.Clone(b)})
	}
}

func (n *Network) remove(ap netip.AddrPort) {
	n.mu.Lock()
	delete(n.conns, ap)
	n.mu.Unlock()
}

type packet struct {
	from netip.AddrPort
	data []byte
}

// PacketConn implements net.PacketConn over the in-memory network.
type PacketConn struct {
	net             *Network
	local           netip.AddrPort
	inbox           chan packet
	closed          chan struct{}
	closeOnce       sync.Once
	mu              sync.Mutex
	readDeadline    time.Time
	deadlineChanged chan struct{}
}

func (c *PacketConn) enqueue(p packet) {
	select {
	case c.inbox <- p:
	case <-c.closed:
	default: // full: dropped, like a UDP socket buffer
	}
}

// ReadFrom implements net.PacketConn.
func (c *PacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, from, err, again := c.readOnce(b)
		if !again {
			return n, from, err
		}
	}
}

// readOnce waits for one packet, the deadline, close, or a deadline change (again = true).
func (c *PacketConn) readOnce(b []byte) (n int, from net.Addr, err error, again bool) {
	select {
	case <-c.closed:
		return 0, nil, net.ErrClosed, false
	default:
	}
	c.mu.Lock()
	dl := c.readDeadline
	c.mu.Unlock()
	var timeout <-chan time.Time
	if !dl.IsZero() {
		d := time.Until(dl)
		if d <= 0 {
			return 0, nil, os.ErrDeadlineExceeded, false
		}
		t := time.NewTimer(d)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case p := <-c.inbox:
		return copy(b, p.data), net.UDPAddrFromAddrPort(p.from), nil, false
	case <-c.closed:
		return 0, nil, net.ErrClosed, false
	case <-c.deadlineChanged:
		return 0, nil, nil, true
	case <-timeout:
		return 0, nil, os.ErrDeadlineExceeded, false
	}
}

// WriteTo implements net.PacketConn.
func (c *PacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	if len(b) > maxPacket {
		return 0, fmt.Errorf("memnet: packet of %d bytes: %w", len(b), ErrBadAddr)
	}
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("memnet: %T: %w", addr, ErrBadAddr)
	}
	to := ua.AddrPort()
	to = netip.AddrPortFrom(to.Addr().Unmap(), to.Port())
	c.net.deliver(c.local, to, b)
	return len(b), nil
}

// Close implements net.PacketConn.
func (c *PacketConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.net.remove(c.local)
	})
	return nil
}

// LocalAddr implements net.PacketConn.
func (c *PacketConn) LocalAddr() net.Addr { return net.UDPAddrFromAddrPort(c.local) }

// SetDeadline implements net.PacketConn (read side only).
func (c *PacketConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

// SetReadDeadline implements net.PacketConn.
func (c *PacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.mu.Unlock()
	select {
	case c.deadlineChanged <- struct{}{}:
	default:
	}
	return nil
}

// SetWriteDeadline implements net.PacketConn; writes never block.
func (c *PacketConn) SetWriteDeadline(time.Time) error { return nil }
