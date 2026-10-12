// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/mixproto/mixcore/driver"
)

// loopbackIP is the only address the demo mixer and its sessions use.
const loopbackIP = "127.0.0.1"

// ErrNotLoopback is returned for a socket that would leave this device: a
// send to a non-loopback address, or a socket the OS did not bind to loopback.
var ErrNotLoopback = errors.New("demo: address is not loopback")

// Listen returns a socket opener for the demo mixer and for sessions to it.
// Every socket is opened with base (nil: the OS, without any socket binder)
// on 127.0.0.1 with the requested port, whatever host was asked for, and is
// guarded: sends to a non-loopback address fail with ErrNotLoopback and
// packets from one are dropped.
func Listen(base driver.ListenPacketFunc) driver.ListenPacketFunc {
	if base == nil {
		base = osListen
	}
	return func(ctx context.Context, _, address string) (net.PacketConn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("demo: listen %q: %w", address, err)
		}
		pc, err := base(ctx, "udp4", net.JoinHostPort(loopbackIP, port))
		if err != nil {
			return nil, err
		}
		if !isLoopback(pc.LocalAddr()) {
			_ = pc.Close()
			return nil, fmt.Errorf("demo: bound to %s: %w", pc.LocalAddr(), ErrNotLoopback)
		}
		return loopbackConn{PacketConn: pc}, nil
	}
}

func osListen(ctx context.Context, network, address string) (net.PacketConn, error) {
	var lc net.ListenConfig
	return lc.ListenPacket(ctx, network, address)
}

// loopbackConn talks to loopback addresses only.
type loopbackConn struct{ net.PacketConn }

// ReadFrom returns the next packet from a loopback address; others are dropped.
func (c loopbackConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, from, err := c.PacketConn.ReadFrom(b)
		if err != nil || isLoopback(from) {
			return n, from, err
		}
	}
}

// WriteTo sends to a loopback address only.
func (c loopbackConn) WriteTo(b []byte, to net.Addr) (int, error) {
	if !isLoopback(to) {
		return 0, fmt.Errorf("demo: send to %s: %w", to, ErrNotLoopback)
	}
	return c.PacketConn.WriteTo(b, to)
}

func isLoopback(a net.Addr) bool {
	ua, ok := a.(*net.UDPAddr)
	return ok && ua.IP.IsLoopback()
}
