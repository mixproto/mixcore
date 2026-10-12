// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
)

const (
	// inboundCap bounds packets waiting for the owner goroutine. A full queue
	// blocks the reader; the socket buffer absorbs bursts.
	inboundCap = 64
	// rxBufSize is larger than any packet this driver asks for (the biggest is
	// a 96-value meter packet of about 400 bytes, X32-F51; an X Air bank 1
	// packet is 104 bytes, XA section 6).
	rxBufSize = 4096
)

type inPacket struct {
	data   []byte // aliases the reader's ring; valid until inboundCap+1 newer packets
	at     time.Time
	viaCmd bool // arrived on the write socket: only re-read replies come there
}

// conn owns the two UDP sockets of a session and their reader goroutines.
type conn struct {
	name      string // family, for errors
	console   netip.AddrPort
	consoleTo net.Addr
	sub       net.PacketConn // /xremote, reads, meters (receives pushes)
	cmd       net.PacketConn // writes only (X32-F23)
	inbound   chan inPacket
	errs      chan error // reader failures other than a normal close
	failed    atomic.Pointer[error]
	done      chan struct{}
	meters    *MeterSink
	base      time.Time    // monotonic origin of lastRx
	lastRx    atomic.Int64 // nanoseconds after base of the last packet from the console
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func openConn(ctx context.Context, prof *Profile, console netip.AddrPort, opts driver.Options) (*conn, error) {
	name := string(prof.Family)
	listen := listenFunc(opts)
	sub, err := listen(ctx, "udp4", anyAddr)
	if err != nil {
		return nil, fmt.Errorf("%s: open subscription socket: %w", name, err)
	}
	cmd, err := listen(ctx, "udp4", anyAddr)
	if err != nil {
		_ = sub.Close()
		return nil, fmt.Errorf("%s: open command socket: %w", name, err)
	}
	c := &conn{
		name: name, console: console, consoleTo: net.UDPAddrFromAddrPort(console),
		sub: sub, cmd: cmd,
		inbound: make(chan inPacket, inboundCap), errs: make(chan error, 2), done: make(chan struct{}),
		meters: NewMeterSink(prof.Meters, opts.Meters), base: time.Now(),
	}
	onPanic := func(err error) { c.fail(err) }
	safe.Go(&c.wg, onPanic, func() { c.fail(c.readLoop(sub, false)) })
	safe.Go(&c.wg, onPanic, func() { c.fail(c.readLoop(cmd, true)) })
	return c, nil
}

func (c *conn) fail(err error) {
	if err == nil {
		return
	}
	c.failed.CompareAndSwap(nil, &err)
	select {
	case c.errs <- err:
	default:
	}
}

// failure returns the first reader failure, or nil while both sockets work.
func (c *conn) failure() error {
	if p := c.failed.Load(); p != nil {
		return *p
	}
	return nil
}

// discardInbound drops packets queued for a session that has ended.
func (c *conn) discardInbound() {
	for {
		select {
		case <-c.inbound:
		default:
			return
		}
	}
}

// readLoop forwards packets from the console. Meter packets are decoded right
// here into a pre-allocated frame and never reach the owner (latest wins).
func (c *conn) readLoop(pc net.PacketConn, isCmd bool) error {
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
			return fmt.Errorf("%s: read: %w", c.name, err)
		}
		if !c.fromConsole(from) {
			i-- // slot unused; ignore strangers on the LAN
			continue
		}
		now := time.Now()
		c.lastRx.Store(int64(now.Sub(c.base))) // any packet proves the downlink (X32-F25)
		pkt := ring[i][:n]
		if !isCmd && c.meters.Handle(pkt, now) {
			i--
			continue
		}
		select {
		case c.inbound <- inPacket{data: pkt, at: now, viaCmd: isCmd}:
		case <-c.done:
			return nil
		}
	}
}

// lastRxAt is when the last packet from the console arrived (base before any).
func (c *conn) lastRxAt() time.Time { return c.base.Add(time.Duration(c.lastRx.Load())) }

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
