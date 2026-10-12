// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package flow turns captured frames into decoded observations. It keeps the
// traffic between the console and its apps (everything else is counted and
// dropped), orders each TCP direction, picks a decoder per flow from the
// first chunks (or the one the operator forced for a console port) and tags
// every message with direction, transport, endpoints and the evidence
// location in the capture file.
package flow

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// Defaults for Config.
const (
	DefaultDetectChunks = 16
	DefaultDetectWindow = 2 * time.Second
	maxFlows            = 4096
)

// Dir is the direction of a message relative to the console.
type Dir uint8

// Directions.
const (
	ToConsole Dir = iota + 1
	FromConsole
)

func (d Dir) String() string {
	if d == ToConsole {
		return "app->console"
	}
	return "console->app"
}

// Evidence locates a message in the session's capture file.
type Evidence struct {
	File   string
	Offset int64 // byte offset of the packet record or block
	Packet int   // 1-based packet number
}

// String is "file:offset", the form reports cite.
func (e Evidence) String() string { return fmt.Sprintf("%s:%d", e.File, e.Offset) }

// Frame is one captured frame handed to the table.
type Frame struct {
	At       time.Time
	LinkType uint16
	Data     []byte
	Evidence Evidence
}

// Observation is one decoded message with its context.
type Observation struct {
	At        time.Time
	Dir       Dir
	Transport netpkt.Transport
	App       netip.AddrPort
	Console   netip.AddrPort
	Decoder   string
	Msg       decode.Message
	Evidence  Evidence
}

// Config configures a Table.
type Config struct {
	Console netip.Addr        // the console's IP address (required)
	Ports   []uint16          // console ports to keep; empty keeps all
	Force   map[uint16]string // console port -> decoder name
	Decode  decode.Options
	// DetectChunks and DetectWindow end detection: a flow's decoder is chosen
	// after this many chunks, or this long after its first chunk.
	DetectChunks int
	DetectWindow time.Duration
}

// ErrNoConsole is returned when Config.Console is not set.
var ErrNoConsole = errors.New("flow: console address required")

// Stats counts what the table saw. Dropped counts messages decoders had to
// abandon (too large or too deep, decode.Dropper), over every flow.
type Stats struct {
	Frames, Skipped, Foreign, Malformed, Gaps int
	Dropped                                   int
}

// FlowInfo describes one flow for reports.
type FlowInfo struct {
	Transport netpkt.Transport
	App       netip.AddrPort
	Console   netip.AddrPort
	Decoder   string
	Framing   string // binary decoder only
	Messages  int
}

// String is a one-line description.
func (f FlowInfo) String() string {
	s := fmt.Sprintf("%s %s <-> %s: %s, %d messages", f.Transport, f.App, f.Console, f.Decoder, f.Messages)
	if f.Framing != "" {
		s += ", framing " + f.Framing
	}
	return s
}

type flowID struct {
	tr           netpkt.Transport
	app, console netip.AddrPort
}

// Table holds the flows of one capture. It is not safe for concurrent use.
type Table struct {
	cfg   Config
	decs  []decode.Decoder
	flows map[flowID]*flow
	order []flowID
	stats Stats
}

// New returns a table for cfg.
func New(cfg Config) (*Table, error) {
	if !cfg.Console.IsValid() {
		return nil, ErrNoConsole
	}
	cfg.Console = cfg.Console.Unmap()
	if cfg.DetectChunks <= 0 {
		cfg.DetectChunks = DefaultDetectChunks
	}
	if cfg.DetectWindow <= 0 {
		cfg.DetectWindow = DefaultDetectWindow
	}
	for port, name := range cfg.Force {
		if _, err := decode.ByName(name, cfg.Decode); err != nil {
			return nil, fmt.Errorf("flow: decoder for port %d: %w", port, err)
		}
	}
	return &Table{cfg: cfg, decs: decode.All(cfg.Decode), flows: map[flowID]*flow{}}, nil
}

// Stats returns the counters so far.
func (t *Table) Stats() Stats {
	st := t.stats
	for _, f := range t.flows {
		for _, s := range f.streams {
			if d, ok := s.(decode.Dropper); ok {
				st.Dropped += d.Dropped()
			}
		}
	}
	return st
}

// Flows describes every flow in order of first appearance.
func (t *Table) Flows() []FlowInfo {
	out := make([]FlowInfo, 0, len(t.order))
	for _, id := range t.order {
		f := t.flows[id]
		info := FlowInfo{Transport: id.tr, App: id.app, Console: id.console, Decoder: "(undecided)", Messages: f.messages}
		if f.dec != nil {
			info.Decoder = f.dec.Name()
			if fr, ok := decode.FramingOf(f.streams[0]); ok && id.tr == netpkt.TCP {
				info.Framing = fr.String()
			}
		}
		out = append(out, info)
	}
	return out
}

// Add dissects one frame and emits the messages it completes.
func (t *Table) Add(fr Frame, emit func(Observation)) {
	t.stats.Frames++
	p, err := netpkt.Dissect(fr.LinkType, fr.Data)
	switch {
	case errors.Is(err, netpkt.ErrSkip):
		t.stats.Skipped++
		return
	case err != nil:
		t.stats.Malformed++
		return
	}
	id, dir, ok := t.classify(p)
	if !ok {
		t.stats.Foreign++
		return
	}
	f := t.flows[id]
	if f == nil {
		if len(t.flows) >= maxFlows {
			t.stats.Foreign++
			return
		}
		f = newFlow(id)
		t.flows[id] = f
		t.order = append(t.order, id)
	}
	for _, c := range f.accept(dir, p, &t.stats) {
		t.chunk(f, pendingChunk{at: fr.At, dir: dir, data: c, ev: fr.Evidence}, emit)
	}
}

// classify finds the console end of a packet: the console's IP and, when
// Config.Ports is set, one of its ports (so an app on the same host, as in
// tests on loopback, is told apart by port).
func (t *Table) classify(p netpkt.Packet) (flowID, Dir, bool) {
	toConsole, fromConsole := t.isConsole(p.Dst), t.isConsole(p.Src)
	switch {
	case toConsole && !fromConsole:
		return flowID{p.Transport, p.Src, p.Dst}, ToConsole, true
	case fromConsole && !toConsole:
		return flowID{p.Transport, p.Dst, p.Src}, FromConsole, true
	default:
		return flowID{}, 0, false
	}
}

func (t *Table) isConsole(ap netip.AddrPort) bool {
	return ap.Addr().Unmap() == t.cfg.Console && (len(t.cfg.Ports) == 0 || slices.Contains(t.cfg.Ports, ap.Port()))
}

func (t *Table) chunk(f *flow, c pendingChunk, emit func(Observation)) {
	if f.dec != nil {
		t.feed(f, c, emit)
		return
	}
	f.pending = append(f.pending, c)
	if len(f.pending) >= t.cfg.DetectChunks {
		t.decide(f, emit)
	}
}

// Tick decides every flow whose detection window ended by now.
func (t *Table) Tick(now time.Time, emit func(Observation)) {
	for _, id := range t.order {
		f := t.flows[id]
		if f.dec == nil && len(f.pending) > 0 && now.Sub(f.pending[0].at) >= t.cfg.DetectWindow {
			t.decide(f, emit)
		}
	}
}

// Flush decides every pending flow (end of capture).
func (t *Table) Flush(emit func(Observation)) {
	for _, id := range t.order {
		if f := t.flows[id]; f.dec == nil && len(f.pending) > 0 {
			t.decide(f, emit)
		}
	}
}

func (t *Table) decide(f *flow, emit func(Observation)) {
	var samples [2][][]byte
	for _, c := range f.pending {
		samples[c.dir-1] = append(samples[c.dir-1], c.data)
	}
	if name, ok := t.cfg.Force[f.id.console.Port()]; ok {
		f.dec, _ = decode.ByName(name, t.cfg.Decode) // validated in New
	} else {
		f.dec = decode.Choose(t.decs, f.id.tr, samples[0], samples[1])
	}
	for d := range f.streams {
		f.streams[d] = f.dec.NewStream(f.id.tr, joinChunks(samples[d]))
	}
	pending := f.pending
	f.pending = nil
	for _, c := range pending {
		t.feed(f, c, emit)
	}
}

func joinChunks(cs [][]byte) []byte {
	var out []byte
	for _, c := range cs {
		out = append(out, c...)
	}
	return out
}

func (t *Table) feed(f *flow, c pendingChunk, emit func(Observation)) {
	if f.reset[c.dir-1] {
		f.reset[c.dir-1] = false
		if d, ok := f.streams[c.dir-1].(decode.Dropper); ok {
			t.stats.Dropped += d.Dropped() // keep the count of the stream being replaced
		}
		f.streams[c.dir-1] = f.dec.NewStream(f.id.tr, nil)
	}
	f.streams[c.dir-1].Feed(c.data, func(m decode.Message) {
		f.messages++
		emit(Observation{
			At: c.at, Dir: c.dir, Transport: f.id.tr, App: f.id.app, Console: f.id.console,
			Decoder: f.dec.Name(), Msg: m, Evidence: c.ev,
		})
	})
}
