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
	return p
}
