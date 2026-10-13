// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"fmt"

	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

const (
	inputChannels = 32   // X32-F30
	nameMaxLen    = 12   // X32-F31
	levelSteps    = 1023 // X32-F41: valid level floats are k/1023
	muteGroups    = 6    // X32-F35
	dcaGroups     = 8    // X32-F37
	muteGroupMask = 63   // X32-F36: 6-bit membership bitmap
	dcaMask       = 255  // X32-F38: 8-bit membership bitmap
	headamps      = 128  // X32-F70: /headamp/000..127
	gainSteps     = 144  // X32-F72: k/144, 0.5 dB
	// gainZero is 0 dB (k = 24), the simulator's initial Gain (a choice).
	gainZero    = float32(24.0 / 144)
	haIndexes   = 32  // X32-F74: /-ha/00..31 = ch 01..32 (32..39 aux in not simulated)
	noHeadamp   = -1  // X32-F74: the source has no headamp
	maxHeadampN = 127 // X32-F70
	// Scene marker range: a simulator choice. X32-F61 documents an index of
	// the cue, scene or snippet lists (0..99), -1 is accepted as "none".
	minScene = -1
	maxScene = 99

	addrUndoTime = "/-undo/time"           // X32-F62
	addrPrepos   = "/-show/prepos/current" // X32-F61

	mixBuses   = 16  // X32-F80: /bus/01..16
	matrices   = 6   // X32-F87: /mtx/01..06
	sendSteps  = 160 // X32-F83: send levels have 161 known values
	panSteps   = 100 // X32-F84: linf -100..100 step 2, 101 values
	panCentre  = 0.5 // X32-F84: f = (pan + 100) / 200
	colourMax  = 15  // X32-F89: config/color 0..15
	sendTapMax = 5   // X32-F85: type 0..5 = IN/LC, <-EQ, EQ->, PRE, POST, GRP
)

// newParams builds the modelled parameter tree. Initial values are a
// simulator choice (not a protocol fact): faders at 0.0 (off), channel and
// DCA switches on, mute groups released, no group membership, empty names.
func newParams() map[string]*oscsim.Value {
	p := map[string]*oscsim.Value{}
	for ch := 1; ch <= inputChannels; ch++ {
		p[fmt.Sprintf("/ch/%02d/config/name", ch)] = oscsim.Text(nameMaxLen)           // X32-F31
		p[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = oscsim.Level(levelSteps)            // X32-F32, X32-F41
		p[fmt.Sprintf("/ch/%02d/mix/on", ch)] = oscsim.Switch(1)                       // X32-F33
		p[fmt.Sprintf("/ch/%02d/grp/mute", ch)] = oscsim.IntRange(0, muteGroupMask, 0) // X32-F36
		p[fmt.Sprintf("/ch/%02d/grp/dca", ch)] = oscsim.IntRange(0, dcaMask, 0)        // X32-F38
	}
	p["/main/st/mix/fader"] = oscsim.Level(levelSteps)            // X32-F32, X32-F34
	p["/main/st/mix/on"] = oscsim.Switch(1)                       // X32-F33, X32-F34
	p["/main/st/grp/mute"] = oscsim.IntRange(0, muteGroupMask, 0) // X32-F36
	for g := 1; g <= muteGroups; g++ {
		p[fmt.Sprintf("/config/mute/%d", g)] = oscsim.Switch(0) // X32-F35: 1 = engaged
	}
	for d := 1; d <= dcaGroups; d++ {
		p[fmt.Sprintf("/dca/%d/on", d)] = oscsim.Switch(1) // X32-F37: 0 = DCA muted
	}
	for h := 0; h < headamps; h++ { // X32-F70: three digits, 0-based
		p[fmt.Sprintf("/headamp/%03d/gain", h)] = oscsim.LevelAt(gainSteps, gainZero) // X32-F71, X32-F72
		p[fmt.Sprintf("/headamp/%03d/phantom", h)] = oscsim.Switch(0)                 // X32-F73: 1 = on
	}
	for ch := 0; ch < haIndexes; ch++ { // default patch (a choice): ch N uses local headamp N
		p[fmt.Sprintf("/-ha/%02d/index", ch)] = oscsim.IntRange(noHeadamp, maxHeadampN, int32(ch))
	}
	p[addrPrepos] = oscsim.IntRange(minScene, maxScene, 0) // X32-F61
	p[addrUndoTime] = oscsim.Text(nameMaxLen)              // X32-F62
	addOutputs(p)
	return p
}

// addOutputs adds buses, sends, matrices, DCA masters and colours
// (x32.md X32-F80..F89). Initial values are a simulator choice: faders and
// send levels at 0.0 (off), switches and sends on, pans centred, taps 0,
// colours 0, links off, empty names.
func addOutputs(p map[string]*oscsim.Value) {
	for ch := 1; ch <= inputChannels; ch++ {
		root := fmt.Sprintf("/ch/%02d", ch)
		p[root+"/config/color"] = oscsim.IntRange(0, colourMax, 0) // X32-F89
		for bus := 1; bus <= mixBuses; bus++ {
			send := fmt.Sprintf("%s/mix/%02d", root, bus) // X32-F82: two digits
			p[send+"/on"] = oscsim.Switch(1)              // X32-F82: 1 = send on
			p[send+"/level"] = oscsim.Level(sendSteps)    // X32-F82, X32-F83
			if bus%2 == 1 {                               // X32-F84, X32-F85: odd sends only
				p[send+"/pan"] = oscsim.LevelAt(panSteps, panCentre)
				p[send+"/type"] = oscsim.IntRange(0, sendTapMax, 0)
			}
		}
	}
	p["/main/st/config/color"] = oscsim.IntRange(0, colourMax, 0) // X32-F89
	for bus := 1; bus <= mixBuses; bus++ {
		addMaster(p, fmt.Sprintf("/bus/%02d", bus)) // X32-F80
	}
	for pair := 1; pair < mixBuses; pair += 2 {
		p[fmt.Sprintf("/config/buslink/%d-%d", pair, pair+1)] = oscsim.Switch(0) // X32-F81: 1 = linked
	}
	for m := 1; m <= matrices; m++ {
		addMaster(p, fmt.Sprintf("/mtx/%02d", m)) // X32-F87
	}
	for d := 1; d <= dcaGroups; d++ {
		root := fmt.Sprintf("/dca/%d", d)
		p[root+"/fader"] = oscsim.Level(levelSteps)                // X32-F88
		p[root+"/config/name"] = oscsim.Text(nameMaxLen)           // X32-F88
		p[root+"/config/color"] = oscsim.IntRange(0, colourMax, 0) // X32-F89
	}
}

// addMaster adds name, colour, fader and mute of a bus or matrix (X32-F80, X32-F87).
func addMaster(p map[string]*oscsim.Value, root string) {
	p[root+"/config/name"] = oscsim.Text(nameMaxLen)
	p[root+"/config/color"] = oscsim.IntRange(0, colourMax, 0)
	p[root+"/mix/fader"] = oscsim.Level(levelSteps)
	p[root+"/mix/on"] = oscsim.Switch(1) // 1 = on, 0 = muted (as X32-F33)
}
