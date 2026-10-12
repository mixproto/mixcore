// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package xair simulates the network side of an X Air / M Air mixer (XR12,
// XR16, XR18, X18, MR12, MR18; compatibility statement only). It is the test
// oracle for driver/xair, so it is written from docs/protocols/xair.md only
// (comments cite fact IDs such as XA-F20) and never imports driver or model
// code. What the doc marks as the same as X32 (transport, /xinfo, /xremote
// lifetime and client table, meter lifetime, write log) is the shared
// sim/internal/oscsim core; this package adds the X Air address tree, port,
// limits, echo rule, /info, /status, int16 meters and snapshots.
//
// Every [TO-VERIFY] behaviour of the doc is an option whose default is the
// documented or recommended one; tests run both variants.
package xair

import (
	"log/slog"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// Documented defaults (docs/protocols/xair.md).
const (
	DefaultPort            = 10024                 // XA-F01
	DefaultSubscriptionTTL = 10 * time.Second      // XA-F20
	DefaultMaxPushClients  = 8                     // XA-F20, XA-F21
	DefaultMeterTTL        = 10 * time.Second      // XA-F56: assumed like X32 (TO-VERIFY item 5)
	DefaultMeterInterval   = 50 * time.Millisecond // XA-F56: assumed like X32 (TO-VERIFY item 5)
	DefaultInputChannels   = 16                    // XA-F71: vendor tables, every model
	DefaultBuses           = 6                     // XA-F71
	DefaultNameMaxLen      = 12                    // section 3.2: X32 rule kept until test item 14
)

// Options configure the simulator. Zero values take the documented defaults.
type Options struct {
	Name     string // device name in /xinfo and /info (XA-F10, XA-F11)
	Model    string // e.g. "XR18", "MR18V2" (XA-F70)
	Firmware string // e.g. "1.22" (XA-F78)
	IP       string // address reported in /xinfo; default: the socket's address

	SubscriptionTTL time.Duration // /xremote lifetime (XA-F20)
	MaxPushClients  int           // XA-F21
	// EvictOldestWhenFull: [TO-VERIFY] XA-F21 ninth-client variant. False
	// (default, as X32-F21) ignores a ninth /xremote client until a slot
	// expires; true evicts the oldest one.
	EvictOldestWhenFull bool
	// NoEchoToSender: [TO-VERIFY] hypothesis H1 of section 2. False (default,
	// XA-F23) pushes a change also to the /xremote client that made it; true
	// behaves like X32 (no push back to the sender).
	NoEchoToSender bool
	// ReplyToSet: [TO-VERIFY] hypothesis H3 of section 2 (XA-F22 read
	// literally): an applied set is also answered to its sender's port,
	// whether or not that port holds /xremote.
	ReplyToSet bool
	// SlotForAnySender: [TO-VERIFY] test item 3: every sending port takes a
	// client-table slot, not only /xremote ports.
	SlotForAnySender bool
	// StandbyWhenNotServed: [TO-VERIFY] XA-F16, test item 2: /status answers
	// "standby" to a port that holds no push slot. False (default): always "active".
	StandbyWhenNotServed bool
	// PushSnapIndex: [TO-VERIFY] XA-F62, test item 13: a snapshot load
	// pushes /-snap/index to subscribers. False (default): only the changed
	// parameters are pushed.
	PushSnapIndex bool
	// InputChannels and Buses: [TO-VERIFY] XA-F72, test item 12. The vendor
	// tables give 16 and 6 for every model (default); XS6/XS11 model an XR12
	// as 12 and 2, an XR16 as 16 and 4. Strips above the count do not answer.
	InputChannels int
	Buses         int
	// NameMaxLen: [TO-VERIFY] longest stored strip name (test item 14).
	NameMaxLen int
	// UnknownAddresses are parameters this mixer does not know: reads get no
	// reply and writes are not applied (XA-F03). A test option.
	UnknownAddresses []string
	MeterTTL         time.Duration // XA-F56
	MeterInterval    time.Duration // XA-F56
	Logger           *slog.Logger
}

func (o Options) withDefaults() Options {
	o.Name = cmpOr(o.Name, "XR18-SIM")
	o.Model = cmpOr(o.Model, "XR18")
	o.Firmware = cmpOr(o.Firmware, "1.22")
	o.SubscriptionTTL = cmpOr(o.SubscriptionTTL, DefaultSubscriptionTTL)
	o.MaxPushClients = cmpOr(o.MaxPushClients, DefaultMaxPushClients)
	o.MeterTTL = cmpOr(o.MeterTTL, DefaultMeterTTL)
	o.MeterInterval = cmpOr(o.MeterInterval, DefaultMeterInterval)
	o.InputChannels = min(cmpOr(o.InputChannels, DefaultInputChannels), DefaultInputChannels)
	o.Buses = min(cmpOr(o.Buses, DefaultBuses), DefaultBuses)
	o.NameMaxLen = cmpOr(o.NameMaxLen, DefaultNameMaxLen)
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
	ClassRead      = oscsim.ClassRead      // parameter read, /xinfo, /info, /status
	ClassSubscribe = oscsim.ClassSubscribe // /xremote, /xremotenfb, /meters
	ClassWrite     = oscsim.ClassWrite     // any message with arguments, including /-snap/ actions
	ClassUnknown   = oscsim.ClassUnknown   // unknown address without arguments
)

// ErrUnknownAddress is returned for an address the simulator does not model.
var ErrUnknownAddress = oscsim.ErrUnknownAddress

// Sim is a running simulator. Create with New, run with Serve. The embedded
// core provides Serve, Pause, Resume, Reboot, Log, Writes, WriteCount,
// Subscribers, Slots, MaxSlotsUsed, SurfaceSet and Value.
type Sim struct {
	*oscsim.Core
	opts   Options
	params map[string]*oscsim.Value
	// Guarded by the core lock (Core.Do).
	bank1 [bank1Values]int16         // raw /meters/1 values, 1/256 dB (XA-F51..F53)
	snaps map[int]map[string]osc.Arg // stored snapshot contents by slot (XA-F60)
}

// New creates a simulator with the documented defaults applied to opts.
func New(opts Options) *Sim {
	opts = opts.withDefaults()
	params := newParams(opts)
	for _, a := range opts.UnknownAddresses {
		delete(params, a)
	}
	s := &Sim{opts: opts, params: params, snaps: map[int]map[string]osc.Arg{}}
	for i := range s.bank1 {
		s.bank1[i] = MeterSilence
	}
	s.Core = oscsim.New(oscsim.Config{
		Prefix: "xair sim", Name: opts.Name, Model: opts.Model, Firmware: opts.Firmware, IP: opts.IP,
		SubscriptionTTL: opts.SubscriptionTTL, MaxPushClients: opts.MaxPushClients,
		EvictOldestWhenFull: opts.EvictOldestWhenFull, EchoToSender: !opts.NoEchoToSender,
		ReplyToSet: opts.ReplyToSet, SlotForAnySender: opts.SlotForAnySender,
		MeterTTL: opts.MeterTTL, MeterInterval: opts.MeterInterval,
		Logger: opts.Logger, Params: params, Family: (*family)(s),
	})
	return s
}
