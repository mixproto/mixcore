// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package oscsim is the part of the console simulators that the X32 and the
// X Air families share, because their protocol documents say it is the same
// (docs/protocols/xair.md section 11): OSC over UDP, read = address without
// arguments, write = address with one argument, replies to the requester's
// address and port, /xinfo, /xremote with a lifetime and a push-client
// limit, meter streams with a lifetime, and the write log. Each family
// package (sim/x32, sim/xair) supplies its parameter tree, its own addresses
// and its meter encoding, citing its own protocol document.
//
// Like the family simulators it never imports driver or model code.
package oscsim

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// Shared addresses (X32-F10, X32-F20; XA-F10, XA-F20).
const (
	AddrXinfo     = "/xinfo"
	AddrXremote   = "/xremote"
	maxPacketSize = 65507
)

// Family is what a family simulator adds to the core. Both methods are called
// with the core lock held and must not call methods that take it.
type Family interface {
	// Special handles an address that is not a plain parameter (/info,
	// /meters, ...). handled=false passes the message on to the parameters.
	Special(now time.Time, from netip.AddrPort, m *osc.Message) (out []Outgoing, handled bool)
	// MeterPacket encodes one meter packet of bank (nil: none).
	MeterPacket(bank string) []byte
}

// Config configures the core. Durations and limits come from the family's
// documented defaults; options marked unverified there are plain flags here.
type Config struct {
	Prefix          string // log and error prefix, e.g. "x32 sim"
	Name            string // /xinfo network name
	Model           string
	Firmware        string
	IP              string // /xinfo address; default: the socket's address
	SubscriptionTTL time.Duration
	MaxPushClients  int
	// EvictOldestWhenFull: a newcomer to a full client table evicts the
	// oldest entry instead of being ignored.
	EvictOldestWhenFull bool
	// EchoToSender: a change is also pushed to its sender when the sender
	// holds /xremote (and did not ask for no feedback).
	EchoToSender bool
	// ReplyToSet: an applied set is answered to its sender's address and
	// port, subscribed or not (a reply, not a push).
	ReplyToSet bool
	// SlotForAnySender: every sending port takes a client-table slot.
	SlotForAnySender bool
	// SilentWrite: a client write to an address it reports true for is
	// applied (and read back) but pushed to nobody and answered to nobody
	// (the "no Gain echo" variant, X32-F79, XA-F116). nil: none.
	SilentWrite   func(addr string) bool
	MeterTTL      time.Duration
	MeterInterval time.Duration
	Logger        *slog.Logger
	Params        map[string]*Value
	Family        Family
}

// Outgoing is one packet to send once the lock is released.
type Outgoing struct {
	To  netip.AddrPort
	Pkt []byte
}

// Core is a running simulator core. Create with New, run with Serve.
type Core struct {
	cfg Config

	mu        sync.Mutex
	pc        net.PacketConn
	subs      []subscriber
	meterSubs []meterSub
	log       []Entry
	nWrites   int
	maxSlots  int
	paused    bool
	downUntil time.Time
}

// ErrUnknownAddress is returned for an address the simulator does not model.
var ErrUnknownAddress = errors.New("sim: unknown address")

// New creates a core. cfg.Logger nil discards logs.
func New(cfg Config) *Core {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Core{cfg: cfg}
}

// Serve answers packets on pc until ctx is cancelled. It closes nothing; the
// caller owns pc. Serve returns nil on cancellation.
func (c *Core) Serve(ctx context.Context, pc net.PacketConn) error {
	c.mu.Lock()
	if c.pc != nil {
		c.mu.Unlock()
		return errors.New(c.cfg.Prefix + ": already serving")
	}
	c.pc = pc
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.pc = nil
		c.mu.Unlock()
	}()

	if err := pc.SetReadDeadline(time.Time{}); err != nil { // a previous Serve may have left one
		return fmt.Errorf("%s: %w", c.cfg.Prefix, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()
	wg.Go(func() { c.meterLoop(ctx) })
	return c.readLoop(ctx, pc)
}

func (c *Core) readLoop(ctx context.Context, pc net.PacketConn) error {
	buf := make([]byte, maxPacketSize)
	for {
		n, from, err := pc.ReadFrom(buf)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return fmt.Errorf("%s: read: %w", c.cfg.Prefix, err)
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		ap := ua.AddrPort()
		c.handlePacket(netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), buf[:n])
	}
}

// Send sends packets; callers must not hold the lock.
func (c *Core) Send(out []Outgoing) {
	c.mu.Lock()
	pc := c.pc
	c.mu.Unlock()
	if pc == nil {
		return
	}
	for _, o := range out {
		if _, err := pc.WriteTo(o.Pkt, net.UDPAddrFromAddrPort(o.To)); err != nil {
			c.cfg.Logger.Debug(c.cfg.Prefix+": send failed", "to", o.To, "err", err)
		}
	}
}

// Do runs fn with the lock held, then sends what it returned. fn may call the
// ...Locked methods.
func (c *Core) Do(fn func(now time.Time) []Outgoing) {
	now := time.Now()
	c.mu.Lock()
	out := fn(now)
	c.mu.Unlock()
	c.Send(out)
}

// OfflineLocked reports whether the console is unreachable. Callers hold the lock.
func (c *Core) OfflineLocked(now time.Time) bool { return c.paused || now.Before(c.downUntil) }

// Pause makes the console unreachable (network loss): packets in and out are
// dropped. State and timers keep running, so subscriptions expire as on real hardware.
func (c *Core) Pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

// Resume ends Pause.
func (c *Core) Resume() {
	c.mu.Lock()
	c.paused = false
	c.mu.Unlock()
}

// Reboot drops every /xremote and /meters registration and makes the console
// unreachable for downFor. Parameter values survive (console state, not
// session state); whether registrations survive is unverified for both
// families (X32-F26, XA-F26).
func (c *Core) Reboot(downFor time.Duration) {
	c.mu.Lock()
	c.subs = nil
	c.meterSubs = nil
	c.downUntil = time.Now().Add(downFor)
	c.mu.Unlock()
}

// ReportedIPLocked is the address /xinfo reports. Callers hold the lock.
func (c *Core) ReportedIPLocked() string {
	if c.cfg.IP != "" {
		return c.cfg.IP
	}
	if c.pc != nil {
		if ap, err := netip.ParseAddrPort(c.pc.LocalAddr().String()); err == nil {
			return ap.Addr().String()
		}
	}
	return "0.0.0.0"
}

// Reply encodes one message to to.
func (c *Core) Reply(to netip.AddrPort, addr string, args ...osc.Arg) []Outgoing {
	pkt, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		c.cfg.Logger.Error(c.cfg.Prefix+": encode reply", "addr", addr, "err", err)
		return nil
	}
	return []Outgoing{{To: to, Pkt: pkt}}
}
