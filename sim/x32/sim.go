// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package x32 simulates the network side of an X32/M32 console. It is the test
// oracle for driver/x32, so it is written from docs/protocols/x32.md only
// (comments cite fact IDs such as X32-F20) and never imports driver or model
// code: an oracle that shared tables with the driver would hide its bugs.
//
// Every packet the simulator receives is classified and logged; Writes() is
// what live-show safety tests assert on.
package x32

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// Documented defaults (docs/protocols/x32.md).
const (
	DefaultPort            = 10023                 // X32-F01
	DefaultSubscriptionTTL = 10 * time.Second      // X32-F20
	DefaultMaxPushClients  = 4                     // X32-F21
	DefaultMeterTTL        = 10 * time.Second      // X32-F50
	DefaultMeterInterval   = 50 * time.Millisecond // X32-F50
	maxPacketSize          = 65507
)

// Options configure the simulator. Zero values take the documented defaults.
type Options struct {
	Name     string // network name in /xinfo (X32-F10)
	Model    string // e.g. "X32" (X32-F11)
	Firmware string
	IP       string // address reported in /xinfo; default: the socket's address

	SubscriptionTTL time.Duration // /xremote lifetime (X32-F20)
	MaxPushClients  int           // X32-F21
	// EvictOldestWhenFull: [UNVERIFIED] X32-F21 variant. False (default) ignores a
	// fifth /xremote client until a slot expires; true evicts the oldest one.
	EvictOldestWhenFull bool
	// EchoToSender: [UNVERIFIED] variant of X32-F23. False (documented
	// behaviour) never pushes a change back to the client that sent it.
	EchoToSender bool
	// SlotForAnySender: [UNVERIFIED] pessimistic reading of X32-F28. False
	// (documented) gives a client-table slot only to ports that sent /xremote;
	// true lets every sending port hold a slot for SubscriptionTTL, although
	// only /xremote ports receive pushes.
	SlotForAnySender bool
	// PushSceneMarkers: [UNVERIFIED] X32-F60 variant. False (default) makes a
	// scene recall push only the changed parameters; true also sets and pushes
	// /-show/prepos/current and /-undo/time (X32-F61, X32-F62).
	PushSceneMarkers bool
	MeterTTL         time.Duration // X32-F50
	MeterInterval    time.Duration // X32-F50
	Logger           *slog.Logger
}

func (o Options) withDefaults() Options {
	o.Name = cmpOr(o.Name, "X32-SIM")
	o.Model = cmpOr(o.Model, "X32")
	o.Firmware = cmpOr(o.Firmware, "4.06")
	o.SubscriptionTTL = cmpOr(o.SubscriptionTTL, DefaultSubscriptionTTL)
	o.MaxPushClients = cmpOr(o.MaxPushClients, DefaultMaxPushClients)
	o.MeterTTL = cmpOr(o.MeterTTL, DefaultMeterTTL)
	o.MeterInterval = cmpOr(o.MeterInterval, DefaultMeterInterval)
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

func cmpOr[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Class is how the simulator classified a received packet.
type Class uint8

// Packet classes for the write log.
const (
	ClassRead      Class = iota + 1 // parameter read, /xinfo, /info, /status
	ClassSubscribe                  // /xremote, /meters, /renew
	ClassWrite                      // any message with arguments to a parameter or unknown address
	ClassUnknown                    // unknown address without arguments
)

func (c Class) String() string {
	return [...]string{"invalid", "read", "subscribe", "write", "unknown"}[min(int(c), 4)]
}

// Entry is one received packet.
type Entry struct {
	At       time.Time
	From     netip.AddrPort
	Class    Class
	Address  string
	Arg      osc.Arg // first argument (writes); Bytes is a private copy
	Accepted bool    // for writes: the value was applied
}

// subscriber is one entry of the console's client table (X32-F21, X32-F28).
type subscriber struct {
	addr      netip.AddrPort
	since     time.Time
	slotUntil time.Time // the entry holds a slot until then
	pushUntil time.Time // pushes go to it until then (its /xremote lifetime)
}

// ClientSlot is one active entry of the simulated client table.
type ClientSlot struct {
	Addr    netip.AddrPort
	Since   time.Time
	Xremote bool // a live /xremote registration (receives pushes)
}

type meterSub struct {
	addr     netip.AddrPort
	bank     string
	expires  time.Time
	interval time.Duration
	next     time.Time
}

// Sim is a running simulator. Create with New, run with Serve.
type Sim struct {
	opts Options

	mu        sync.Mutex
	pc        net.PacketConn
	params    map[string]*value
	subs      []subscriber
	meterSubs []meterSub
	levels    [inputChannels]float32 // linear input meter levels (X32-F54)
	mainLR    [2]float32             // linear Main L/R meter levels (X32-F55)
	log       []Entry
	nWrites   int
	maxSlots  int
	paused    bool
	downUntil time.Time
}

// ErrUnknownAddress is returned by SurfaceSet for an address the simulator does not model.
var ErrUnknownAddress = errors.New("x32 sim: unknown address")

// New creates a simulator with the documented defaults applied to opts.
func New(opts Options) *Sim {
	return &Sim{opts: opts.withDefaults(), params: newParams()}
}

// Serve answers packets on pc until ctx is cancelled. It closes nothing; the
// caller owns pc. Serve returns nil on cancellation.
func (s *Sim) Serve(ctx context.Context, pc net.PacketConn) error {
	s.mu.Lock()
	if s.pc != nil {
		s.mu.Unlock()
		return errors.New("x32 sim: already serving")
	}
	s.pc = pc
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.pc = nil
		s.mu.Unlock()
	}()

	if err := pc.SetReadDeadline(time.Time{}); err != nil { // a previous Serve may have left one
		return fmt.Errorf("x32 sim: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()
	wg.Go(func() { s.meterLoop(ctx) })
	return s.readLoop(ctx, pc)
}

func (s *Sim) readLoop(ctx context.Context, pc net.PacketConn) error {
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
			return fmt.Errorf("x32 sim: read: %w", err)
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		ap := ua.AddrPort()
		s.handlePacket(netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), buf[:n])
	}
}

func (s *Sim) send(to netip.AddrPort, pkt []byte) {
	s.mu.Lock()
	pc := s.pc
	s.mu.Unlock()
	if pc == nil {
		return
	}
	if _, err := pc.WriteTo(pkt, net.UDPAddrFromAddrPort(to)); err != nil {
		s.opts.Logger.Debug("x32 sim: send failed", "to", to, "err", err)
	}
}

// offline reports whether the simulated console is unreachable. Callers hold s.mu.
func (s *Sim) offline(now time.Time) bool { return s.paused || now.Before(s.downUntil) }

// Pause makes the console unreachable (network loss): packets in and out are
// dropped. State and timers keep running, so subscriptions expire as on real hardware.
func (s *Sim) Pause() {
	s.mu.Lock()
	s.paused = true
	s.mu.Unlock()
}

// Resume ends Pause.
func (s *Sim) Resume() {
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
}

// Reboot drops every /xremote and /meters registration and makes the console
// unreachable for downFor (X32-F26, [UNVERIFIED]: registrations do not survive a reboot).
// Parameter values survive (they are console state, not session state).
func (s *Sim) Reboot(downFor time.Duration) {
	s.mu.Lock()
	s.subs = nil
	s.meterSubs = nil
	s.downUntil = time.Now().Add(downFor)
	s.mu.Unlock()
}

// Log returns a copy of every received packet entry.
func (s *Sim) Log() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

// Writes returns the received write packets, in arrival order.
func (s *Sim) Writes() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.log {
		if e.Class == ClassWrite {
			out = append(out, e)
		}
	}
	return out
}

// WriteCount returns the number of writes received so far (cheap; for polling).
func (s *Sim) WriteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nWrites
}

// Subscribers returns the addresses of active /xremote clients.
func (s *Sim) Subscribers() []netip.AddrPort {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var out []netip.AddrPort
	for _, sub := range s.subs {
		if now.Before(sub.pushUntil) {
			out = append(out, sub.addr)
		}
	}
	return out
}

// Slots returns the active entries of the client table (X32-F28).
func (s *Sim) Slots() []ClientSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var out []ClientSlot
	for _, sub := range s.subs {
		if now.Before(sub.slotUntil) {
			out = append(out, ClientSlot{Addr: sub.addr, Since: sub.since, Xremote: now.Before(sub.pushUntil)})
		}
	}
	return out
}

// MaxSlotsUsed is the largest number of client-table slots that were in use
// at the same time since New.
func (s *Sim) MaxSlotsUsed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxSlots
}

// SetMainLevels sets the linear Main L/R meter levels of bank 2 (X32-F55).
func (s *Sim) SetMainLevels(l, r float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mainLR = [2]float32{l, r}
}

// SetInputLevels sets the linear meter levels of input channels 1..n (1.0 = 0 dBFS, X32-F54).
func (s *Sim) SetInputLevels(levels []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy(s.levels[:], levels)
}
