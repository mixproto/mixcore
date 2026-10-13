// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package wing simulates the network side of a Behringer WING console
// (WING, WING Rack, WING Compact; compatibility statement only): the native
// protocol on TCP 2222, discovery on UDP 2222 and meters by UDP. It is the
// test oracle of driver/wing, written from docs/protocols/wing.md only
// (comments cite WG-F ids and section 12 "What sim/wing needs"); it never
// imports the driver, its codec or the model.
//
// Every [TO-VERIFY] behaviour of the document is an Option whose default is
// the documented or most plausible one; tests run both variants.
//
// Hashes: the document lists a few real hashes (WG-F32) and the fixture uses
// them; every other node has a placeholder hash, because the console's hash
// function is not documented and a client must learn hashes from the
// console's own definitions (WG-F33), which is what the simulator tests.
package wing

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Documented defaults (docs/protocols/wing.md).
const (
	DefaultPort          = 2222                  // TCP control, UDP discovery (WG-F10)
	DefaultIdleTimeout   = 10 * time.Second      // WG-F25
	DefaultMeterInterval = 50 * time.Millisecond // WG-F51
	DefaultMeterTTL      = 5 * time.Second       // WG-F51
	maxConns30           = 16                    // WG-F22
	maxConns31           = 24                    // WG-F09
	// LocalGainStepCoarse and LocalGainStepFine are the two local gain laws
	// of WG-F62 (hardware item 14).
	LocalGainStepCoarse = 2.5
	LocalGainStepFine   = 0.5
	outQueue            = 4096 // queued writes per connection before it is dropped
	// defaultGroupMuteValue: $mute is 0..2 (wing.md 3.2); what 2 means is
	// item 12, so the default reports it for a mute by a group.
	defaultGroupMuteValue = 2
)

// RefuseMode is how a connection over the limit is refused (item 7).
type RefuseMode uint8

// Refuse modes.
const (
	RefuseClose RefuseMode = iota // accept, then close with a FIN (default)
	RefuseReset                   // accept, then reset (RST) where the transport allows it
)

// Options configure the simulator. Zero values take the documented defaults.
type Options struct {
	Name     string // consolename and discovery name (WG-F11)
	Model    string // $cnsmdl, e.g. "ngc-full", "wing-compact", "wing-rack" (WG-F04)
	Firmware string // $firmware, e.g. "3.0.6-0-g...:release"; 3.1 and later: 24 clients, 18 colours (WG-F09)
	Serial   string
	IP       string // address reported by discovery; default: the UDP socket's

	// MaxConnections: 16, or 24 from firmware 3.1 (WG-F22, WG-F09).
	MaxConnections int
	Refuse         RefuseMode    // item 7
	IdleTimeout    time.Duration // WG-F25: no byte received for this long closes the connection

	// EchoToWriter: [TO-VERIFY] item 4. False (default): a client's own
	// write is not pushed back to it.
	EchoToWriter bool
	// NoPushOtherClients: [TO-VERIFY] item 5. False (default): a client's
	// write is pushed to every other connection, like a desk change (WG-F20).
	NoPushOtherClients bool
	// TCPLockAnswersOld: [TO-VERIFY] item 8. While /$syscfg/tcplock = 1 a
	// write is ignored (default); with this option the unchanged value is
	// pushed back to the writer.
	TCPLockAnswersOld bool
	// DataEndsWithDE: [TO-VERIFY] item 3. True: every data answer (DC) ends
	// with DE, like a definition answer. Default false.
	DataEndsWithDE bool
	// RoundFaderDB: [TO-VERIFY] item 10. 0 (default) keeps dB writes as
	// written; 0.1 rounds them to 0.1 dB.
	RoundFaderDB float64
	// LocalGainStep: LocalGainStepCoarse (default, the console's own
	// definition) or LocalGainStepFine (the document's table), item 14.
	LocalGainStep float64
	// PushSceneMarkers: [TO-VERIFY] item 17. A scene load also pushes
	// /$ctl/lib/$actidx and $active. Default false.
	PushSceneMarkers bool

	// GroupMuteValue: [TO-VERIFY] item 12. The $mute a strip reports when
	// only a mute group or a DCA mutes it: 2 (default, a value whose
	// meaning the document leaves open) or 1.
	GroupMuteValue int32
	// UnknownPaths are leaves this console does not have (a schema
	// difference, WG-F35): they are not in the tree at all.
	UnknownPaths []string

	MeterInterval time.Duration // WG-F51
	MeterTTL      time.Duration // WG-F51
	Logger        *slog.Logger
}

func (o Options) withDefaults() Options {
	o.Name = cmpOr(o.Name, "WING-SIM")
	o.Model = cmpOr(o.Model, "ngc-full")
	o.Firmware = cmpOr(o.Firmware, "3.0.6-0-g00000000:release")
	o.Serial = cmpOr(o.Serial, "SIM0000001")
	if o.MaxConnections == 0 {
		o.MaxConnections = maxConns30
		if o.is31() {
			o.MaxConnections = maxConns31
		}
	}
	o.IdleTimeout = cmpOr(o.IdleTimeout, DefaultIdleTimeout)
	o.LocalGainStep = cmpOr(o.LocalGainStep, LocalGainStepCoarse)
	o.MeterInterval = cmpOr(o.MeterInterval, DefaultMeterInterval)
	o.MeterTTL = cmpOr(o.MeterTTL, DefaultMeterTTL)
	o.GroupMuteValue = cmpOr(o.GroupMuteValue, defaultGroupMuteValue)
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// is31 reports firmware 3.1 or later (WG-F09).
func (o Options) is31() bool {
	f := o.Firmware
	return !strings.HasPrefix(f, "1.") && !strings.HasPrefix(f, "2.") && !strings.HasPrefix(f, "3.0")
}

func cmpOr[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// ErrUnknownPath is returned for a path the fixture does not have.
var ErrUnknownPath = errors.New("wing sim: unknown path")

// Sim is a simulated console. Create with New; serve TCP with Serve or
// ServeConn and UDP (discovery, meters) with ServeUDP.
type Sim struct {
	opts Options

	mu        sync.Mutex
	tree      *tree
	conns     map[*client]struct{}
	nextID    int
	maxUsed   int
	refused   int
	log       []Entry
	paused    bool
	downUntil time.Time
	pc        net.PacketConn
	levels    meterLevels
	members   map[int]uint32 // channel -> mute groups (bit g-1), test control
}

// New returns a simulator with the fixture tree.
func New(opts Options) *Sim {
	o := opts.withDefaults()
	t := buildFixture(o)
	for _, p := range o.UnknownPaths {
		t.remove(p)
	}
	return &Sim{opts: o, tree: t, conns: map[*client]struct{}{}, levels: newMeterLevels(), members: map[int]uint32{}}
}

// Serve accepts TCP connections on ln until ctx ends or ln fails.
func (s *Sim) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Go(func() { s.ServeConn(ctx, c) })
	}
}

// ServeConn runs one client connection until it closes, ctx ends, the idle
// timeout passes, or the simulator drops it. Over the connection limit it is
// refused at once (WG-F22).
func (s *Sim) ServeConn(ctx context.Context, c net.Conn) {
	cl, ok := s.admit(c)
	if !ok {
		refuse(c, s.opts.Refuse)
		return
	}
	stop := context.AfterFunc(ctx, cl.drop)
	defer stop()
	cl.run()
	s.mu.Lock()
	delete(s.conns, cl)
	s.mu.Unlock()
}

// admit registers a connection, or refuses it over the limit or while down.
func (s *Sim) admit(c net.Conn) (*client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) >= s.opts.MaxConnections || time.Now().Before(s.downUntil) {
		s.refused++
		return nil, false
	}
	s.nextID++
	cl := newClient(s, c, s.nextID)
	s.conns[cl] = struct{}{}
	s.maxUsed = max(s.maxUsed, len(s.conns))
	return cl, true
}

func refuse(c net.Conn, mode RefuseMode) {
	if tc, ok := c.(*net.TCPConn); ok && mode == RefuseReset {
		_ = tc.SetLinger(0)
	}
	_ = c.Close()
}

// Connections is the number of open client connections.
func (s *Sim) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// MaxConnectionsUsed is the largest number of connections open at once.
func (s *Sim) MaxConnectionsUsed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxUsed
}

// Refused counts connections refused over the limit or while down.
func (s *Sim) Refused() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused
}

// DropConnections closes every client connection with a FIN (a fault).
func (s *Sim) DropConnections() {
	for _, cl := range s.clients() {
		cl.drop()
	}
}

// Reboot drops every connection and meter subscription and refuses new
// connections for downFor (section 12 "Faults"; item 20). Values stay.
func (s *Sim) Reboot(downFor time.Duration) {
	s.mu.Lock()
	s.downUntil = time.Now().Add(downFor)
	s.mu.Unlock()
	s.DropConnections()
}

// Pause stalls the console: connections stay open, nothing is answered or
// pushed and no meters are sent, and the idle timeout does not run.
func (s *Sim) Pause() { s.mu.Lock(); s.paused = true; s.mu.Unlock() }

// Resume ends Pause.
func (s *Sim) Resume() { s.mu.Lock(); s.paused = false; s.mu.Unlock() }

func (s *Sim) clients() []*client {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*client, 0, len(s.conns))
	for cl := range s.conns {
		out = append(out, cl)
	}
	return out
}

// Hash returns the hash of a path (for tests).
func (s *Sim) Hash(path string) (uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.tree.byPath[path]
	if !ok {
		return 0, false
	}
	return n.hash, true
}

// reportIP is the address discovery reports.
func (s *Sim) reportIP(local net.Addr) string {
	if s.opts.IP != "" {
		return s.opts.IP
	}
	if ua, ok := local.(*net.UDPAddr); ok {
		return ua.AddrPort().Addr().Unmap().String()
	}
	return netip.IPv4Unspecified().String()
}
