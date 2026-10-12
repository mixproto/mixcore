// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package x32 drives Behringer X32 / Midas M32 family consoles over OSC/UDP
// (compatibility statement only). It is implemented from
// docs/protocols/x32.md; comments cite fact IDs such as X32-F20.
//
// The session engine (two sockets, echo matching, liveness, client-limit
// detection, background re-read, scene heuristic) is shared with the X Air
// family in driver/internal/xosc, whose package doc describes it. This
// package is the X32 profile: port 10023 (X32-F01), 32 inputs, /main/st,
// 6 mute groups, 8 DCAs (X32-F30..F38), four push clients (X32-F21), meter
// banks 1 and 2 as linear float32 (X32-F51..F55) and the scene markers
// /-undo/time and /-show/prepos/current (X32-F61, X32-F62).
//
// Echo behaviour: the console does not push a change back to the client that
// sent it (X32-F23), so writes leave from a second socket and come back as
// pushes on the /xremote socket (EchoesOwnWrites = true). Nothing in the
// driver writes in response to a received packet.
package x32

import (
	"errors"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/internal/xosc"
	"github.com/mixproto/mixcore/model"
)

// Family is the family name of this driver.
const Family driver.Family = "x32"

// Timing holds every interval of the session (X32-F20, X32-F25, X32-F27, X32-F50).
type Timing = xosc.Timing

// DefaultTiming is used for every zero field of Config.Timing.
var DefaultTiming = xosc.DefaultTiming

// Config configures the driver.
type Config struct {
	Timing Timing
}

// Driver implements driver.Driver and driver.LinkOpener for the X32 family.
type Driver struct {
	*xosc.Driver
}

var (
	_ driver.Driver     = (*Driver)(nil)
	_ driver.LinkOpener = (*Driver)(nil)
)

// ErrBadEndpoint is returned for an endpoint that is not an IP address.
var ErrBadEndpoint = errors.New("x32: endpoint must be an IP address")

// New returns a driver. Zero fields of Config.Timing take DefaultTiming.
func New(cfg Config) *Driver {
	return &Driver{Driver: xosc.New(profile, cfg.Timing)}
}

// headamps (x32.md X32-F70..F76). Local 000..031: writable, phantom read,
// probed (16 answer on an X32 COMPACT). AES50 A and B: read-only until
// hardware item 21 shows what a write to a box this console may not own
// does (lead decision 2026-10-12), and read only while a channel's /-ha
// index points at them (X32-F74). Phantom is never written (X32-F73).
var headamps = []xosc.HeadampGroup{
	{Bank: model.BankLocal, First: 1, Count: localHeadamps, Role: xosc.HeadampUnit, Writable: true, Phantom: true, Law: gainLaw},
	{Bank: model.BankAES50A, First: localHeadamps + 1, Count: aes50Headamps, Role: xosc.HeadampPatched, Law: gainLaw},
	{Bank: model.BankAES50B, First: localHeadamps + aes50Headamps + 1, Count: aes50Headamps, Role: xosc.HeadampPatched, Law: gainLaw},
}

var gainLaw = xosc.HeadampLaw{MinDB: gainMinDB, StepDB: gainStepDB, Steps: gainSteps}

// profile is the X32 family for the shared engine.
var profile = xosc.Profile{
	Family:         Family,
	DefaultPort:    DefaultPort,
	MaxPushClients: maxPushClients,
	Layout: xosc.Layout{
		Inputs: inputChannels, RequiredInputs: inputChannels, MainRoot: "/main/st", MainMuteGroups: true,
		MuteGroups: muteGroupCount, DCAs: dcaCount,
		Headamps: headamps, HeadampAddr: headampFormat, HeadampWireOffset: headampWireOff, HeadampIndexes: inputChannels,
	},
	Meters: meterCodec{},
	Markers: []xosc.Marker{
		{Addr: addrUndoTime, Kind: xosc.MarkerRecall, Signal: driver.SignalUndoTime}, // X32-F62
		{Addr: addrPrepos, Kind: xosc.MarkerHighlight},                               // X32-F61: highlighted, not loaded
	},
	BadEndpoint: ErrBadEndpoint,
}
