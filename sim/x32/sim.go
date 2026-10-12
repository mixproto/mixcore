// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package x32 simulates the network side of an X32/M32 console. It is the test
// oracle for driver/x32, so it is written from docs/protocols/x32.md only
// (comments cite fact IDs such as X32-F20) and never imports driver or model
// code: an oracle that shared tables with the driver would hide its bugs.
// What X32 and X Air share (transport, /xinfo, /xremote, client table, meter
// lifetime, write log) lives in sim/internal/oscsim.
//
// Every packet the simulator receives is classified and logged; Writes() is
// what live-show safety tests assert on.
package x32

import (
	"log/slog"
	"time"

	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// Documented defaults (docs/protocols/x32.md).
const (
	DefaultPort            = 10023                 // X32-F01
	DefaultSubscriptionTTL = 10 * time.Second      // X32-F20
	DefaultMaxPushClients  = 4                     // X32-F21
	DefaultMeterTTL        = 10 * time.Second      // X32-F50
	DefaultMeterInterval   = 50 * time.Millisecond // X32-F50
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
	// UnknownAddresses are parameters this console does not know (older
	// firmware, another model): reads get no reply and writes are not
	// applied (X32-F06). A test option, not a documented fact.
	UnknownAddresses []string
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

// Log entry types, shared with every simulator (sim/internal/oscsim).
type (
	Class      = oscsim.Class
	Entry      = oscsim.Entry
	ClientSlot = oscsim.ClientSlot
)

// Packet classes for the write log.
const (
	ClassRead      = oscsim.ClassRead      // parameter read, /xinfo, /info
	ClassSubscribe = oscsim.ClassSubscribe // /xremote, /meters
	ClassWrite     = oscsim.ClassWrite     // any message with arguments to a parameter or unknown address
	ClassUnknown   = oscsim.ClassUnknown   // unknown address without arguments
)

// ErrUnknownAddress is returned by SurfaceSet for an address the simulator does not model.
var ErrUnknownAddress = oscsim.ErrUnknownAddress

// Sim is a running simulator. Create with New, run with Serve. The embedded
// core provides Serve, Pause, Resume, Reboot, Log, Writes, WriteCount,
// Subscribers, Slots, MaxSlotsUsed, SurfaceSet and Value.
type Sim struct {
	*oscsim.Core
	opts   Options
	params map[string]*oscsim.Value
	// Guarded by the core lock (Core.Do).
	levels [inputChannels]float32 // linear input meter levels (X32-F54)
	mainLR [2]float32             // linear Main L/R meter levels (X32-F55)
}

// New creates a simulator with the documented defaults applied to opts.
func New(opts Options) *Sim {
	opts = opts.withDefaults()
	params := newParams()
	for _, a := range opts.UnknownAddresses {
		delete(params, a)
	}
	s := &Sim{opts: opts, params: params}
	s.Core = oscsim.New(oscsim.Config{
		Prefix: "x32 sim", Name: opts.Name, Model: opts.Model, Firmware: opts.Firmware, IP: opts.IP,
		SubscriptionTTL: opts.SubscriptionTTL, MaxPushClients: opts.MaxPushClients,
		EvictOldestWhenFull: opts.EvictOldestWhenFull, EchoToSender: opts.EchoToSender,
		SlotForAnySender: opts.SlotForAnySender, MeterTTL: opts.MeterTTL, MeterInterval: opts.MeterInterval,
		Logger: opts.Logger, Params: params, Family: (*family)(s),
	})
	return s
}

// SetMainLevels sets the linear Main L/R meter levels of bank 2 (X32-F55).
func (s *Sim) SetMainLevels(l, r float32) {
	s.Do(func(time.Time) []oscsim.Outgoing {
		s.mainLR = [2]float32{l, r}
		return nil
	})
}

// SetInputLevels sets the linear meter levels of input channels 1..n (1.0 = 0 dBFS, X32-F54).
func (s *Sim) SetInputLevels(levels []float32) {
	s.Do(func(time.Time) []oscsim.Outgoing {
		copy(s.levels[:], levels)
		return nil
	})
}
