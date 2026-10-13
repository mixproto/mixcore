// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package xair drives Behringer X Air (XR12, XR16, XR18, X18) and Midas M Air
// (MR12, MR18) mixers, V2 units included, over OSC/UDP (compatibility
// statement only). It is implemented from docs/protocols/xair.md; comments
// cite fact IDs such as XA-F20.
//
// The protocol is the X32 dialect (xair.md section 11), so the session engine
// is the one driver/x32 uses (driver/internal/xosc); this package is the
// X Air profile:
//   - port 10024 (XA-F01); /xinfo identity and discovery as X32 (XA-F10,
//     XA-F12); the family is told apart by reply port and model (IsModel).
//   - Echo (XA-F22, XA-F23): with /xremote the mixer is documented to push a
//     change to its sender too, and possibly to answer a set to its sender.
//     The two-socket session is correct under all three readings of the
//     sources (xair.md section 2 "Echo design"): writes leave from the
//     command socket and their echo arrives as a push on the /xremote socket;
//     a stray echo on the command socket is dropped because only replies to
//     an outstanding, untouched re-read are accepted there. EchoesOwnWrites is
//     true. Nothing writes in response to a received packet.
//   - Client limit: 8 /xremote clients (XA-F21). The ninth gets no pushes and
//     no signal; the same evidence as X32 raises ClientLimitHit{Limit: 8}.
//   - Subscriptions: /xremote lasts 10 s (XA-F20) and is renewed every 3 s
//     with the meter request; liveness, stale (1.5 s without a packet) and
//     uplink checks (3 s without an /xinfo reply) are the X32 rules (XA-F25).
//   - Strips (section 3): 16 inputs /ch/01..16, main /lr with no mute-group
//     membership, 4 mute groups, 4 DCAs. Inputs 13..16 are optional: a small
//     model that does not answer them connects with fewer inputs (XA-F72).
//   - Headamps (xair.md 3.6): mic /headamp/01..16, dB = -12 + 72 f, k/144,
//     writable, phantom read (XA-F111, XA-F113); fewer on XR16 (8) and XR12
//     (4), probed once per link (XA-F73). Aux /headamp/17 on the 18-channel
//     models only, line law dB = -12 + 32 f, k/64, read-only because the law
//     is unconfirmed (XA-F112). XR16/XR12 line inputs are absent: their
//     numbering is unverified (XA-F110). The wire maximum 1.0 is never
//     written (XA-F116). Which channel a headamp feeds (insrc) is unverified
//     (XA-F115), so Gain is per headamp strip, not per channel.
//   - Meters: one bank, /meters/1, int16 LE in 1/256 dB; frame = 16 inputs
//     then Main L/R from values 36/37 (XA-F51..F53).
//   - Buses and sends (core-api v0.4, sections 3.1-3.3): buses /bus/1..6
//     (pairs 1-2, 3-4, 5-6 linkable) and FX sends /fxsend/1..4 as buses
//     7..10; sends /ch/NN/mix/MM/level (161 steps), tap per send, pan per
//     send on buses 1..6 only (no send pan to FX sends, 3.3, item 10),
//     no send on/off (absent, never faked). Every bus is probed once per
//     link, so a model with fewer buses connects without them (XA-F72).
//     DCA faders and names, and colours, are read; tap and colours are
//     read-only.
//   - Scene recall: no dedicated message; a burst of pushes raises
//     SceneRecalled (low). /-snap/index names the recently loaded or saved
//     snapshot (XA-F60..F62): a save moves it too, so it never raises a
//     recall alone and only adds LoadedScene to a burst within 1 s. The
//     driver never reads or writes any other /-snap/ address, so snapshot 64
//     is never touched (XA-F63).
package xair

import (
	"errors"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/internal/xosc"
	"github.com/mixproto/mixcore/model"
)

// Family is the family name of this driver.
const Family driver.Family = "xair"

// Timing holds every interval of the session (XA-F20, XA-F25: the X32 values).
type Timing = xosc.Timing

// DefaultTiming is used for every zero field of Config.Timing.
var DefaultTiming = xosc.DefaultTiming

// Config configures the driver.
type Config struct {
	Timing Timing
}

// Driver implements driver.Driver and driver.LinkOpener for the X Air family.
type Driver struct {
	*xosc.Driver
}

var (
	_ driver.Driver     = (*Driver)(nil)
	_ driver.LinkOpener = (*Driver)(nil)
)

// ErrBadEndpoint is returned for an endpoint that is not an IP address.
var ErrBadEndpoint = errors.New("xair: endpoint must be an IP address")

// New returns a driver. Zero fields of Config.Timing take DefaultTiming.
func New(cfg Config) *Driver {
	return &Driver{Driver: xosc.New(profile, cfg.Timing)}
}

// headamps (xair.md 3.6): mic 01..16, fewer on XR16/XR12 (probed), writable
// on the mic law (XA-F111, lead decision); aux 17 on the 18-channel models
// only, read-only because the line law is unconfirmed (XA-F112, item 22).
// XR16/XR12 line inputs are absent: their numbering is unverified (XA-F110).
var headamps = []xosc.HeadampGroup{
	{Bank: model.BankLocal, First: 1, Count: micHeadamps, Role: xosc.HeadampUnit, Writable: true, Phantom: true,
		Law: xosc.HeadampLaw{MinDB: gainMinDB, StepDB: gainStepDB, Steps: micGainSteps}},
	{Bank: model.BankAux, First: auxHeadamp, Count: 1, Role: xosc.HeadampExtra,
		Law: xosc.HeadampLaw{MinDB: gainMinDB, StepDB: gainStepDB, Steps: auxGainSteps}},
}

// profile is the X Air family for the shared engine.
var profile = xosc.Profile{
	Family:         Family,
	DefaultPort:    DefaultPort,
	MaxPushClients: maxPushClients,
	Layout: xosc.Layout{
		Inputs: inputChannels, RequiredInputs: requiredInputs, MainRoot: "/lr", MainMuteGroups: false,
		MuteGroups: muteGroupCount, DCAs: dcaCount,
		Headamps: headamps, HeadampAddr: headampFormat,
		Buses: []xosc.BusGroup{
			// Section 3.1, 3.2 buslink, 3.3 send pan per send on buses 1..6.
			{First: 1, Count: mixBuses, Root: "/bus/%d", Kind: model.BusMix, Linkable: true, SendPan: true},
			// Section 3.1: FX sends as buses 7..10; 3.3 (2026-10-13): no send pan to them, item 10.
			{First: mixBuses + 1, Count: fxSends, Root: "/fxsend/%d", Kind: model.BusFX},
		},
		SendPan: true, SendTapLeaf: "tap", Colours: true, DCAMasters: true, // section 3.2, 3.3
	},
	// Section 3.3: tap 0..5 = IN, PREEQ, POSTEQ, PRE, POST, GRP.
	TapLabels: [model.TapCount]string{"IN", "PREEQ", "POSTEQ", "PRE", "POST", "GRP"},
	Meters:    meterCodec{},
	Markers: []xosc.Marker{{
		// XA-F60: "recently loaded/saved", 1..64: a hint, never a recall alone.
		Addr: addrSnapIndex, Kind: xosc.MarkerIndexHint, Signal: driver.SignalSnapshotIndex,
		MinScene: 1, MaxScene: snapshots,
	}},
	BadEndpoint: ErrBadEndpoint,
}
