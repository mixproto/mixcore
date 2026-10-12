// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Gesture window numbers, dybyskz-mix docs/design/phone-safety.md (decided
// by the lead after the sound-engineer review, open question 36). Design
// values, not protocol facts.
const (
	slipStepDB        = 6.0   // SR-26: one gesture stops 6 dB above its start, then 6 dB per opened wall
	slipFloorDB       = -16.0 // SR-26: below -16 dB the start counts as -16 dB (from -inf the first stop is -10)
	gainCapUpDB       = 3.0   // SR-24: one Gain gesture moves at most +3 dB ...
	gainCapDnDB       = 6.0   // ... and -6 dB from where it started
	gainBoundDB       = 6.0   // SR-31: no Gain write moves more than 6 dB from the console value
	bigRaiseDB        = 6.0   // SR-30: a typed raise of more than 6 dB asks once
	defaultHeadroomDB = 6.0   // SR-28: monitor ceiling headroom default
	// wallDwell is how long the value must stand at a slip-guard wall before
	// openWall is accepted (tokens.md wall.dwell, SR-26; review MEDIUM 4).
	wallDwell = 300 * time.Millisecond
	// The finger rests while it stays within restSpanPt of the track
	// (wall.restSpeed 30 pt/s x wall.dwell 300 ms, phone-safety.md 4.1)
	// when the UI gave the track length, else within restSpanDB of fader
	// travel below the wall (lead decisions: re-review LOW C, pass 3
	// MEDIUM 2; 2 dB still stops a finger pushed at 12.5 dB/s).
	restSpanPt = 9.0
	restSpanDB = 2.0
)

// GestureState is what the core did with a candidate (CR-22).
type GestureState uint8

// Gesture states.
const (
	StateFree      GestureState = iota // applied as asked
	StateAtWall                        // stopped at the slip-guard wall; opens after a rest
	StateAtCap                         // stopped at a Gain cap; never opens
	StateAtCeiling                     // stopped at the monitor ceiling; never opens
	StateLocked                        // show mode stopped a Gain raise
)

func (s GestureState) String() string {
	return [...]string{"free", "atWall", "atCap", "atCeiling", "locked"}[min(int(s), 4)]
}

// Window is the gesture window of one hold (CR-22). Values in dB.
type Window struct {
	Start       float64
	HasWall     bool
	WallUp      float64 // slip guard: the next upward stop
	HasCap      bool
	CapUp       float64 // Gain: start + 3 dB within the range
	CapDown     float64 // Gain: start - 6 dB within the range
	HasCeiling  bool
	Ceiling     float64 // monitor: engineer's latest value + headroom
	OpensByRest bool
}

// window is the owner state of one hold's window. Guarded by Supervisor.mu.
type window struct {
	Window
	law     driver.FaderLaw // nil: re-anchor in dB (Gain, or a driver without a law)
	offset  float64         // re-anchoring after openWall, in fader positions
	state   GestureState
	lastAt  float64   // fader position of the candidate that stopped at the wall
	wallAt  time.Time // when the current rest at the wall began (zero: not at it)
	restAt  float64   // raw finger position where the current rest began
	trackPt float64   // on-screen track length, 0 = unknown (HoldOptions)
}

// slipWall is the first slip-guard stop above start (SR-26).
func slipWall(start float64) float64 { return math.Max(start, slipFloorDB) + slipStepDB }

// clampFader applies a fader candidate (dB): re-anchored by the opened
// walls, stopped at the wall and at the ceiling, never stopped downwards.
func (w *window) clampFader(candidate float64) (float64, GestureState) {
	pos := w.position(candidate)
	v := candidate
	if w.offset != 0 {
		v = w.level(pos - w.offset)
	}
	st := StateFree
	if w.HasWall && v >= w.WallUp { // arriving at the wall counts (one rigid haptic, SR-26)
		v, st, w.lastAt = w.WallUp, StateAtWall, pos
		if w.wallAt.IsZero() || math.Abs(pos-w.restAt) > w.restSpan() { // the finger moved: the rest starts again (re-review LOW C)
			w.wallAt, w.restAt = time.Now(), pos
		}
	} else {
		w.wallAt = time.Time{}
	}
	if w.HasCeiling && v > w.Ceiling {
		v, st = w.Ceiling, StateAtCeiling
	}
	w.state = st
	return v, st
}

// clampGain applies a Gain candidate inside [CapDown, CapUp]; the caps never open.
func (w *window) clampGain(candidate float64) (float64, GestureState) {
	v, st := candidate, StateFree
	switch {
	case v >= w.CapUp: // arriving at the cap counts (SR-25)
		v, st = w.CapUp, StateAtCap
	case v <= w.CapDown:
		v, st = w.CapDown, StateAtCap
	}
	w.state = st
	return v, st
}

// openWall re-anchors after a rest at the wall: the next upward move
// continues from the wall value (no jump) and the next wall is 6 dB higher.
// It reports false where walls do not open by rest, the value is not at the
// wall, or the finger has not been still for wallDwell (the core checks the
// rest itself, so a UI bug cannot open walls at will).
func (w *window) openWall() bool {
	if !w.OpensByRest || w.state != StateAtWall || time.Since(w.wallAt) < wallDwell {
		return false
	}
	w.wallAt = time.Time{}
	// lastAt is the raw finger position (no offset applied), so the new
	// offset replaces the old one: adding them counted the first twice
	// (review HIGH 2).
	w.offset = w.lastAt - w.position(w.WallUp)
	w.WallUp += slipStepDB
	w.state = StateFree
	return true
}

// restSpan is how far the finger may move at the wall and still rest, in
// fader positions: 2 dB of fader travel just below the wall, or 9 pt of the
// track when its length is known and that is smaller. A track length can
// only make the check stricter (review pass 4, MEDIUM 3).
func (w *window) restSpan() float64 {
	span := w.position(w.WallUp) - w.position(w.WallUp-restSpanDB)
	if w.trackPt > 0 && w.law != nil {
		span = math.Min(span, restSpanPt/w.trackPt)
	}
	return span
}

// position maps dB to the fader position the UI's finger moves linearly
// (re-anchoring keeps the finger's ratio); without a law it is dB itself.
func (w *window) position(db float64) float64 {
	if w.law == nil {
		return db
	}
	return w.law.PositionFromDB(db)
}

func (w *window) level(pos float64) float64 {
	if w.law == nil {
		return pos
	}
	return w.law.DBFromPosition(pos)
}
