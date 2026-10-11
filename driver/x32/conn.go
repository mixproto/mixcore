// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
)

const (
	// inboundCap bounds packets waiting for the owner goroutine. A full queue
	// blocks the reader; the socket buffer absorbs bursts.
	inboundCap = 64
	// rxBufSize is larger than any packet this driver asks for (the biggest is
	// a 96-value meter packet of about 400 bytes, X32-F51).
	rxBufSize = 4096
)

type inPacket struct {
	data []byte // aliases the reader's ring; valid until inboundCap+1 newer packets
	at   time.Time
}

// conn owns the two UDP sockets of a session and their reader goroutines.
type conn struct {
	console   netip.AddrPort
	consoleTo net.Addr
	sub       net.PacketConn // /xremote, reads, meters (receives pushes)
	cmd       net.PacketConn // writes only (X32-F23)
	inbound   chan inPacket
	errs      chan error // reader failures other than a normal close
	done      chan struct{}
	meters    *meterSink
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func openConn(ctx context.Context, console netip.AddrPort, opts driver.Options) (*conn, error) {
	listen := listenFunc(opts)
	sub, err := listen(ctx, "udp4", anyAddr)
	if err != nil {
		return nil, fmt.Errorf("x32: open subscription socket: %w", err)
	}
	cmd, err := listen(ctx, "udp4", anyAddr)
	if err != nil {
		_ = sub.Close()
		return nil, fmt.Errorf("x32: open command socket: %w", err)
	}
	c := &conn{
		console: console, consoleTo: net.UDPAddrFromAddrPort(console),
		sub: sub, cmd: cmd,
		inbound: make(chan inPacket, inboundCap), errs: make(chan error, 2), done: make(chan struct{}),
		meters: newMeterSink(opts.Meters),
	}
	onPanic := func(err error) { c.fail(err) }
	safe.Go(&c.wg, onPanic, func() { c.fail(c.readLoop(sub, true)) })
	safe.Go(&c.wg, onPanic, func() { c.fail(c.readLoop(cmd, false)) })
	return c, nil
}

func (c *conn) fail(err error) {
	if err == nil {
		return
	}
	select {
	case c.errs <- err:
	default:
	}
}

// readLoop forwards packets from the console. Meter packets are decoded right
// here into a pre-allocated frame and never reach the owner (latest wins).
func (c *conn) readLoop(pc net.PacketConn, withMeters bool) error {
	ring := make([][]byte, inboundCap+2) // see inPacket.data: the owner's packet is never overwritten
	for i := range ring {
		ring[i] = make([]byte, rxBufSize)
	}
	for i := 0; ; i = (i + 1) % len(ring) {
		n, from, err := pc.ReadFrom(ring[i])
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("x32: read: %w", err)
		}
		if !c.fromConsole(from) {
			i-- // slot unused; ignore strangers on the LAN
			continue
		}
		now := time.Now()
		pkt := ring[i][:n]
		if withMeters && c.meters.handle(pkt, now) {
			i--
			continue
		}
		select {
		case c.inbound <- inPacket{data: pkt, at: now}:
		case <-c.done:
			return nil
		}
	}
}

func (c *conn) fromConsole(a net.Addr) bool {
	ua, ok := a.(*net.UDPAddr)
	if !ok {
		return false
	}
	ap := ua.AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()) == c.console
}

func (c *conn) sendSub(pkt []byte) error {
	_, err := c.sub.WriteTo(pkt, c.consoleTo)
	return err
}

func (c *conn) sendCmd(pkt []byte) error {
	_, err := c.cmd.WriteTo(pkt, c.consoleTo)
	return err
}

// close stops the readers and waits for them. Idempotent.
func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.sub.Close()
		_ = c.cmd.Close()
		c.wg.Wait()
	})
}
