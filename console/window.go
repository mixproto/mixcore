// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

// Gesture window numbers, dybyskz-mix docs/design/phone-safety.md (decided
// by the lead after the sound-engineer review, open question 36). Design
// values, not protocol facts. The slip guard (SR-26) and the Gain caps of
// one gesture (SR-24) are retired (redesign CC-1, CC-2, core-api v0.5):
// no control stops a deliberate move inside its range.
const (
	gainCapUpDB       = 3.0 // SR-24: one Gain gesture moves at most +3 dB ...
	gainCapDnDB       = 6.0 // ... and -6 dB from where it started
	gainBoundDB       = 6.0 // SR-31: no Gain write moves more than 6 dB from the console value
	bigRaiseDB        = 6.0 // SR-30: a typed raise of more than 6 dB asks once
	defaultHeadroomDB = 6.0 // SR-28: monitor ceiling headroom default
)

// GestureState is what the core did with a candidate (CR-22).
type GestureState uint8

// Gesture states. atWall (the slip guard) is retired in core-api v0.5.
const (
	StateFree      GestureState = iota // applied as asked
	StateAtCap                         // Gain stopped at a range end of its headamp
	StateAtCeiling                     // stopped at the monitor ceiling; never opens
	StateLocked                        // show mode stopped a Gain raise
)

func (s GestureState) String() string {
	return [...]string{"free", "atCap", "atCeiling", "locked"}[min(int(s), 3)]
}

// Window is the gesture window of one hold (CR-22). Values in dB.
type Window struct {
	Start      float64
	HasCap     bool
	CapUp      float64 // Gain: the top of the window
	CapDown    float64 // Gain: the bottom of the window
	HasCeiling bool
	Ceiling    float64 // monitor: engineer's latest value + headroom
}

// window is the owner state of one hold's window. Guarded by Supervisor.mu.
type window struct {
	Window
	state GestureState
}

// clampFader applies a fader or send-level candidate (dB): only a monitor
// ceiling stops it, and downward nothing does.
func (w *window) clampFader(candidate float64) (float64, GestureState) {
	v, st := candidate, StateFree
	if w.HasCeiling && v > w.Ceiling {
		v, st = w.Ceiling, StateAtCeiling
	}
	w.state = st
	return v, st
}

// clampGain applies a Gain candidate inside [CapDown, CapUp].
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
