// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"
)

// Review of core-api v0.5 (go-reviewer blockers 1 and 2, should-fix 3 and
// 4; lead decisions): who may settle a Gain hold, and what bounds a write.

// stepBounded checks that every Gain write is within 6 dB of the previous
// one, whichever client sent it.
func stepBounded(t *testing.T, drv *fakeDriver) {
	t.Helper()
	w := gainWrites(drv)
	for i := 1; i < len(w); i++ {
		if math.Abs(w[i]-w[i-1]) > gainBoundDB+1e-9 {
			t.Fatalf("write %d jumps %.1f dB (%v)", i, w[i]-w[i-1], w)
		}
	}
}

// Blocker 1: a second client grabbing the same headamp ends the first
// client's hold (SR-15), and its writes are bounded against what is in
// flight, so no write jumps.
func TestGain_SecondClientHoldEndsTheFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(gain3, gesture, true)
		for _, db := range []float64{33.5, 35.5, 37.5} { // no echo
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
		}
		_ = sup.Drain()
		if err := sup.Hold(gain3, engineerB, true); err != nil {
			t.Fatal(err)
		}
		u := sup.Drain()
		if len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange || u.Events[0].ClientID != gesture.ClientID {
			t.Fatalf("the first hold must end: %+v", u.Events)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 39.5), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("the first client's finger moves on: %v", err)
		}
		for _, db := range []float64{50, 55, 59.5} { // the second client asks for big jumps
			_, _ = sup.Write(set(gain3, lvl(t, db), engineerB))
		}
		stepBounded(t, drv)
	})
}

// Blocker 1, reports: another client's echo is never the holder's own.
func TestGain_AnotherClientsEchoEndsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(gain3, gesture, true)
		_, _ = sup.Write(set(gain3, lvl(t, 33.5), gesture))
		_, _ = sup.Write(set(gain3, lvl(t, 34.5), engineerB)) // a stepper on another device, in flight too
		_ = sup.Drain()
		pushGain(t, drv, 34.5, engineerB, false) // its echo
		if u := sup.Drain(); len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange {
			t.Fatalf("another client's echo must end the hold: %+v", u.Events)
		}
	})
}

// Blocker 2 (the reviewer's probe): a desk change mid-drag to a value one
// of the drag's writes had is still the desk.
func TestGain_DeskValueMatchingAnInFlightWriteEndsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(gain3, gesture, true)
		for _, db := range []float64{34.5, 38.5, 42.5, 38.5, 34.5} { // up to 42.5 and back, no echo
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
		}
		_ = sup.Drain()
		pushGain(t, drv, 42.5, desk, false) // the desk sets 42.5
		if u := sup.Drain(); len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange {
			t.Fatalf("the desk must end the hold: %+v", u.Events)
		}
	})
}

// Should-fix 3 (the reviewer's probe): an authoritative read-back is the
// console's value and is always published (kept as our own when it is one
// of our writes), so a later stepper never works from a stale value.
func TestGain_AuthoritativeReadBackIsAlwaysPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(gain3, gesture, true)
		for db := 33.5; db <= 45.5; db++ { // the last write, 45.5, is lost
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
		}
		_ = sup.Drain()
		pushGain(t, drv, 44.5, desk, true) // the settle reply: the console is at 44.5
		u := sup.Drain()
		if len(u.Events) != 0 {
			t.Fatalf("our own write read back ended the hold: %+v", u.Events)
		}
		if v, _ := u.State.Value(gain3); dbOf(v) != 44.5 {
			t.Fatalf("state %v, want the console's 44.5", dbOf(v))
		}
		_ = sup.Hold(gain3, gesture, false)
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("a stepper from a stale +32.5 wrote 33.5 under a console at 44.5: %v", err)
		}
	})
}

// Should-fix 4 (fader-and-gain.md 15, CC-3 "the first from the console
// value"): the first write of a hold is bounded against the console value
// and every value in flight, not only against the hold's start.
func TestGain_FirstWriteOfAHoldIsBoundedByTheConsoleValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		for _, db := range []float64{30.5, 36.5} { // two steppers, no echo
			if _, err := sup.Write(set(gain3, lvl(t, db), macro)); err != nil {
				t.Fatal(err)
			}
		}
		_ = sup.Hold(gain3, gesture, true)
		if r, _ := sup.Write(set(gain3, lvl(t, 42), gesture)); dbIn(t, r) > 36.5 {
			t.Fatalf("first write %v: more than 6 dB above 30.5 in flight", dbIn(t, r))
		}
		stepBounded(t, drv)
	})
}
