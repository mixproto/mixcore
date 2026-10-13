// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"math"
	"testing"
	"testing/synctest"
	"time"

	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Replays the CR-27 touch paths (AC-PS-01..06) through a test double of the
// UI gesture machine (relative, one locked axis, constant ratio, SR-21..23)
// into the real engine and the X Air simulator, and through the three legacy
// mappings of AC-PS-06 that the paths must catch.

// Geometry and tokens (dybyskz-mix docs/design/phone-safety.md 1.2, 1.3,
// docs/design/tokens.md).
const (
	trackPortrait   = 490.0 // iPhone 6.1" fader track, pt
	trackLandscape  = 152.0
	barPortrait     = 361.0 // the v0.3 Gain bar, pt: the legacy doubles of AC-PS-06 only
	barLandscape    = 343.0
	wheelPortrait   = 240.0 // gain.wheel on a 6.1" phone (fader-and-gain.md 5.1)
	wheelLandscape  = 191.0
	wheelHalfHeight = 32.0 // gain.wheel height 64
	wheelFrameSlop  = 16.0 // gain.wheel.frameSlop: the frame reaches 16 pt above and below
	lockAxisPt      = 10.0 // gesture.lockAxis
	encoderPitchPt  = 48.0 // gain.encoderPitch: 1 dB per 48 pt
	gainMinDB       = -12.0
	gainSpanDB      = 72.0
	speedupPtS      = 250.0 // AC-PS-06 double (c)
	rangePerPt      = 1.0 / 300
	knobSweepDeg    = 270.0
	knobMinAngleDeg = 225.0
	knobCentreY     = 40.0
	writeTol        = 0.05 // dB: a written value is on the console's nearest step
)

type controlKind uint8

const (
	ctlFader controlKind = iota
	ctlGain
)

// run is one replay of one path on one control through our mapping.
type run struct {
	writes  []float64 // console values written, in order (simulator log)
	stops   int       // arrivals at a stop: a Gain range end or a ceiling (rigid haptic)
	firstAt int       // ms of the first write, -1 for none
}

func (r run) max() float64 {
	m := math.Inf(-1)
	for _, w := range r.writes {
		m = math.Max(m, w)
	}
	return m
}

func (r run) min() float64 {
	m := math.Inf(1)
	for _, w := range r.writes {
		m = math.Min(m, w)
	}
	return m
}

func (r run) last() float64 {
	if len(r.writes) == 0 {
		return math.NaN()
	}
	return r.writes[len(r.writes)-1]
}

type uiDouble struct {
	t      *testing.T
	e      *xairEnv
	kind   controlKind
	length float64 // track length, or the Gain wheel's width
	grabX  float64 // Gain: the touch-down point, pt from the wheel's left end
	ended  bool    // Gain: the touch left the wheel's frame (SR-32)
	start  float64 // dB at the grab
	axis   int     // 0 undecided, 1 the control's axis, 2 the other (a scroll)
	state  string
	run    run
}

func (u *uiDouble) param() string {
	if u.kind == ctlGain {
		return gain3
	}
	return vox1
}

func (u *uiDouble) gesture(cmd string, at int) gestureResultJSON {
	out, err := u.e.eng.Gesture(cmd)
	if err != nil {
		u.t.Fatalf("t=%d %s: %v", at, cmd, err)
	}
	var r gestureResultJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		u.t.Fatal(err)
	}
	if r.State != "free" && r.State != "" && u.state != r.State {
		u.run.stops++
	}
	u.state = r.State
	if r.Written && u.run.firstAt < 0 {
		u.run.firstAt = at
	}
	return r
}

// candidate is the UI's relative, one-axis, constant-ratio value (SR-22, SR-23).
func (u *uiDouble) candidate(p pathPoint) float64 {
	if u.kind == ctlGain {
		steps := math.Trunc(p.X / encoderPitchPt)
		return math.Min(math.Max(u.start+steps, gainMinDB), gainMinDB+gainSpanDB)
	}
	pos := math.Min(math.Max(u.e.eng.FaderPosition(u.start)+p.Y/u.length, 0), 1)
	return u.e.eng.FaderDB(pos)
}

func (u *uiDouble) lockAxis(p pathPoint) {
	if u.axis != 0 || math.Hypot(p.X, p.Y) < lockAxisPt {
		return
	}
	horizontal := math.Abs(p.X) > math.Abs(p.Y)
	if horizontal == (u.kind == ctlGain) {
		u.axis = 1
		return
	}
	u.axis = 2 // the other axis: the sheet or bank scrolls, the control never writes
	u.gesture(cmdJSON("holdEnd", u.param(), ""), 0)
}

// play sends one path the way the UI would, sample by sample, in real time.
func (u *uiDouble) play(v pathVariant, large bool) {
	if large {
		return // SR-21: a large contact never grabs
	}
	u.gesture(cmdJSON("holdBegin", u.param(), ""), 0)
	last, prev := math.NaN(), v.Points[0]
	for _, p := range v.Points[1:] {
		time.Sleep(time.Duration(p.T-prev.T) * time.Millisecond)
		if u.leftFrame(p) {
			continue // the stroke ended; this touch writes nothing more
		}
		u.lockAxis(p)
		if u.axis == 1 {
			if c := u.candidate(p); c != last {
				u.gesture(cmdJSON("set", u.param(), `,"value":`+levelText(c)), p.T)
				last = c
			}
		}
		prev = p
	}
	if u.axis != 2 && !u.ended {
		u.gesture(cmdJSON("holdEnd", u.param(), ""), prev.T)
	}
}

// leftFrame applies the Gain wheel's frame rule (SR-32): when the touch
// leaves the wheel's frame the stroke ends (hold-end, light haptic) and
// coming back over the wheel does not resume it.
func (u *uiDouble) leftFrame(p pathPoint) bool {
	if u.kind != ctlGain || u.ended {
		return u.ended
	}
	x := u.grabX + p.X
	if x >= 0 && x <= u.length && math.Abs(p.Y) <= wheelHalfHeight+wheelFrameSlop {
		return false
	}
	u.ended = true
	if u.axis != 2 {
		u.gesture(cmdJSON("holdEnd", u.param(), ""), p.T)
	}
	return true
}

func levelText(db float64) string {
	b, _ := json.Marshal(levelJSON(db))
	return string(b)
}

// replay runs one path on a fresh engine and simulator (Vox1 -6 dB, headamp
// 3 +32.5 dB; the v0.3 phone setting slipGuard on, ignored since v0.5) and
// collects the console writes.
func replay(t *testing.T, kind controlKind, length float64, v pathVariant, large bool) run {
	return replayFrom(t, kind, length, length/2, v, large)
}

// replayFrom is replay with the Gain touch-down grabX pt from the wheel's
// left end (replay: the wheel's centre).
func replayFrom(t *testing.T, kind controlKind, length, grabX float64, v pathVariant, large bool) run {
	var out run
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{"slipGuard":true}`)
		u := &uiDouble{t: t, e: e, kind: kind, length: length, grabX: grabX, run: run{firstAt: -1}}
		if kind == ctlGain {
			u.start = 32.5
		} else {
			f, _ := e.sim.Value("/ch/01/mix/fader") // -6 dB on the console's nearest step
			u.start = e.eng.FaderDB(float64(f.Float))
		}
		u.play(v, large)
		synctest.Wait()
		addr := map[controlKind]string{ctlFader: "/ch/01/mix/fader", ctlGain: "/headamp/03/gain"}[kind]
		for _, w := range e.sim.Writes() {
			if w.Address != addr {
				continue
			}
			if kind == ctlGain {
				u.run.writes = append(u.run.writes, gainMinDB+gainSpanDB*float64(w.Arg.Float))
			} else {
				u.run.writes = append(u.run.writes, e.eng.FaderDB(float64(w.Arg.Float)))
			}
		}
		out = u.run
	})
	return out
}
