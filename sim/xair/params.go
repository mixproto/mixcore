// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"fmt"

	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// Wire facts of docs/protocols/xair.md section 3 and 4.
const (
	fxReturns    = 4  // /rtn/1..4 (section 3.1)
	fxSends      = 4  // /fxsend/1..4 = send slots 07..10 (section 3.1, 3.3)
	dcaGroups    = 4  // /dca/1..4 (section 3.1)
	muteGroups   = 4  // /config/mute/1..4 (section 3.1)
	groupMask    = 15 // grp/mute and grp/dca bitmaps 0..15 (section 3.2)
	faderSteps   = 1023
	sendSteps    = 160 // XA-F41: send levels have 161 steps
	panSteps     = 100 // section 4: pan has 101 steps
	colourMax    = 15  // section 3.2: config/color 0..15
	tapMax       = 5   // section 3.3: IN, PREEQ, POSTEQ, PRE, POST, GRP
	snapshots    = 64  // XA-F60
	snapNameLen  = 31  // XA-F60
	panCentre    = 0.5
	addrSnapIdx  = "/-snap/index" // XA-F60, XA-F61: dashed form used by every client
	addrSnapLoad = "/-snap/load"
	addrSnapSave = "/-snap/save"
	addrSnapDel  = "/-snap/delete"
)

// newParams builds the modelled address tree (section 3.1-3.3). Initial
// values are a simulator choice: faders at 0.0 (off), switches on, pans
// centred, groups released, no membership, empty names, snapshot 1 loaded.
func newParams(o Options) map[string]*oscsim.Value {
	p := map[string]*oscsim.Value{}
	for ch := 1; ch <= o.InputChannels; ch++ {
		addSendingStrip(p, fmt.Sprintf("/ch/%02d", ch), o) // two digits (section 3.1)
	}
	addSendingStrip(p, "/rtn/aux", o) // aux in 17-18 / USB return, one stereo strip
	for r := 1; r <= fxReturns; r++ {
		addSendingStrip(p, fmt.Sprintf("/rtn/%d", r), o) // one digit, unpadded
	}
	for b := 1; b <= o.Buses; b++ {
		root := fmt.Sprintf("/bus/%d", b) // unpadded (XS7 section 8, "no padding")
		addMaster(p, root, o, shapeBus)
		addMembership(p, root)
	}
	for f := 1; f <= fxSends; f++ {
		root := fmt.Sprintf("/fxsend/%d", f)
		addMaster(p, root, o, shapeFXSend) // no pan (section 3.1)
		addMembership(p, root)
	}
	addMaster(p, "/lr", o, shapeMain) // main: no grp/mute, no grp/dca (section 3.2)
	for d := 1; d <= dcaGroups; d++ {
		root := fmt.Sprintf("/dca/%d", d)
		p[root+"/config/name"] = oscsim.Text(o.NameMaxLen)
		p[root+"/fader"] = oscsim.Level(faderSteps) // DCA fader has no mix/ (section 3.2)
		p[root+"/on"] = oscsim.Switch(1)            // 0 = DCA muted
	}
	for g := 1; g <= muteGroups; g++ {
		p[fmt.Sprintf("/config/mute/%d", g)] = oscsim.Switch(0) // 1 = engaged
	}
	for pair := 1; pair < DefaultInputChannels; pair += 2 {
		p[fmt.Sprintf("/config/chlink/%d-%d", pair, pair+1)] = oscsim.Switch(0)
	}
	for pair := 1; pair < DefaultBuses; pair += 2 {
		p[fmt.Sprintf("/config/buslink/%d-%d", pair, pair+1)] = oscsim.Switch(0)
	}
	addSnapshots(p)
	return p
}

// strip shape: which optional master parameters a strip has (section 3.1, 3.2).
type shape struct{ pan, mainAssign bool }

var (
	shapeSending = shape{pan: true, mainAssign: true} // ch, rtn/aux, rtn/N
	shapeBus     = shape{pan: true, mainAssign: true}
	shapeFXSend  = shape{}          // no pan, no main assign in the sources
	shapeMain    = shape{pan: true} // /lr: balance, no grp/*
)

// addMaster adds name, colour, fader and mute, plus pan and main assign by shape.
func addMaster(p map[string]*oscsim.Value, root string, o Options, sh shape) {
	p[root+"/config/name"] = oscsim.Text(o.NameMaxLen)
	p[root+"/config/color"] = oscsim.IntRange(0, colourMax, 0)
	p[root+"/mix/fader"] = oscsim.Level(faderSteps) // XA-F40, XA-F41
	p[root+"/mix/on"] = oscsim.Switch(1)            // 1 = on, 0 = muted
	if sh.pan {
		p[root+"/mix/pan"] = oscsim.LevelAt(panSteps, panCentre)
	}
	if sh.mainAssign {
		p[root+"/mix/lr"] = oscsim.Switch(1) // main assign (section 3.2)
	}
}

func addMembership(p map[string]*oscsim.Value, root string) {
	p[root+"/grp/mute"] = oscsim.IntRange(0, groupMask, 0)
	p[root+"/grp/dca"] = oscsim.IntRange(0, groupMask, 0)
}

// addSendingStrip adds an input-like strip: master, membership and sends to
// slots 01..06 (buses) and 07..10 (FX sends), section 3.3. There is no
// per-send on/off (section 3.3, test item 10).
func addSendingStrip(p map[string]*oscsim.Value, root string, o Options) {
	addMaster(p, root, o, shapeSending)
	addMembership(p, root)
	for slot := 1; slot <= DefaultBuses+fxSends; slot++ {
		if slot <= DefaultBuses && slot > o.Buses {
			continue // a small model without that bus
		}
		send := fmt.Sprintf("%s/mix/%02d", root, slot) // always two digits
		p[send+"/level"] = oscsim.Level(sendSteps)
		p[send+"/pan"] = oscsim.LevelAt(panSteps, panCentre)
		p[send+"/tap"] = oscsim.IntRange(0, tapMax, 0)
		p[send+"/grpon"] = oscsim.Switch(0)
	}
}

// addSnapshots adds /-snap/index and the per-slot names (XA-F60, XA-F61).
// load, save and delete are actions handled in handle.go.
func addSnapshots(p map[string]*oscsim.Value) {
	p[addrSnapIdx] = oscsim.IntRange(1, snapshots, 1)
	for n := 1; n <= snapshots; n++ {
		p[fmt.Sprintf("/-snap/%02d/name", n)] = oscsim.Text(snapNameLen)
	}
}
