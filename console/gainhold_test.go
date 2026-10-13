// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"
)

// Redesign CC-2 and CC-3 (core-api v0.5 rules 4, 7, 8; AC-RD-G01, G03,
// G04): a Gain hold's window is the headamp's range, each write inside a
// hold is bounded against the previous write of the hold, the in-flight
// count never stalls a drag, and the drag's own writes read back never
// end the hold or move the state back.

// AC-RD-G01, AC-RD-G04: no cap inside the range; atCap only at writeMaxDB.
func TestGain_WindowIsTheHeadampRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		r, err := sup.HoldWindow(gain3, gesture, true)
		if err != nil || !r.Window.HasCap || r.Window.CapUp != 59.5 || r.Window.CapDown != -12 {
			t.Fatalf("window %+v %v, want the mic range -12..+59.5", r.Window, err)
		}
		for db := 33.5; db <= 59.5; db++ { // two strokes and more, one step at a time, echoed
			r, err := sup.Write(set(gain3, lvl(t, db), gesture))
			want := StateFree
			if db == 59.5 {
				want = StateAtCap
			}
			if err != nil || dbIn(t, r) != db || r.State != want {
				t.Fatalf("candidate %v: %v %s %v, want %s", db, dbIn(t, r), r.State, err, want)
			}
			pushGain(t, drv, db, gesture, false)
		}
		if r, _ := sup.Write(set(gain3, lvl(t, 60), gesture)); dbIn(t, r) != 59.5 || r.State != StateAtCap {
			t.Fatalf("past the top: %v %s", dbIn(t, r), r.State)
		}
	})
}

// AC-RD-G03 core part: a console that never echoes Gain; 20 one-step
// candidates at 40 per second are all written in order, each within 6 dB
// of the previous write, with no atCap before the range end.
func TestGain_NoEchoDragNeverStalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for i := 1; i <= 20; i++ {
			r, err := sup.Write(set(gain3, lvl(t, 32.5+float64(i)), gesture))
			if err != nil || r.State != StateFree || !r.Written || dbIn(t, r) != 32.5+float64(i) {
				t.Fatalf("step %d: %v %s written %t %v", i, dbIn(t, r), r.State, r.Written, err)
			}
			time.Sleep(25 * time.Millisecond)
		}
		w := gainWrites(drv)
		if len(w) != 20 {
			t.Fatalf("%d writes %v, want 20", len(w), w)
		}
		for i := 1; i < len(w); i++ {
			if w[i] <= w[i-1] || w[i]-w[i-1] > gainBoundDB {
				t.Fatalf("write %d: %v after %v", i, w[i], w[i-1])
			}
		}
	})
}

// Rule 8: inside a hold a candidate is bounded against the previous write
// of that hold, not against the console value.
func TestGain_HoldBoundIsThePreviousWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for _, tc := range []struct{ cand, want float64 }{{37.5, 37.5}, {45, 43.5}, {20, 37.5}} {
			if r, err := sup.Write(set(gain3, lvl(t, tc.cand), gesture)); err != nil || dbIn(t, r) != tc.want {
				t.Fatalf("candidate %v: %v %v, want %v", tc.cand, dbIn(t, r), err, tc.want)
			}
		}
	})
}

// AC-RD-G03 settle part: reports of the drag's own writes (the latest, or
// an earlier one in flight) never end the hold; an earlier one never moves
// the state back; a value matching none of them ends it (SR-15).
func TestGain_SettleReportsOfOwnWritesKeepTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for _, db := range []float64{33.5, 34.5, 35.5} {
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
		}
		_ = sup.Drain()
		pushGain(t, drv, 34.5, desk, true) // an earlier write read back
		if u := sup.Drain(); len(u.Events) != 0 || len(u.Changes) != 0 {
			t.Fatalf("earlier own write: events %+v changes %+v, want none", u.Events, u.Changes)
		}
		if v, _ := sup.Snapshot().State.Value(gain3); dbOf(v) != 32.5 {
			t.Fatalf("state moved to %v", dbOf(v))
		}
		_, _ = sup.Write(set(gain3, lvl(t, 36.5), gesture))
		pushGain(t, drv, 36.5, desk, true) // the latest write read back
		if u := sup.Drain(); len(u.Events) != 0 {
			t.Fatalf("own latest write ended the hold: %+v", u.Events)
		}
		if r, err := sup.Write(set(gain3, lvl(t, 37.5), gesture)); err != nil || dbIn(t, r) != 37.5 {
			t.Fatalf("the drag goes on: %v %v", dbIn(t, r), err)
		}
		pushGain(t, drv, 45, desk, false) // the desk
		if u := sup.Drain(); len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange {
			t.Fatalf("a foreign value must end the hold: %+v", u.Events)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("after the external change: %v", err)
		}
	})
}

// SR-31 unchanged outside a hold: +7 dB from the console value in one write
// is refused.
func TestGain_OutsideAHoldTheBoundIsUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 39.5), gesture)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("+7 dB outside a hold: %v", err)
		}
		if len(drv.session(0).writes()) != 0 {
			t.Fatal("a refused write reached the console")
		}
	})
}

// A new stroke right after one on a console that has not reported yet
// starts from the latest value sent (where the console is going), so the
// next stroke never jumps back to the stale console value.
func TestGain_NextStrokeStartsFromTheValueInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for _, db := range []float64{33.5, 34.5, 35.5, 36.5} {
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
		}
		_ = sup.Hold(gain3, gesture, false)
		r, err := sup.HoldWindow(gain3, gesture, true)
		if err != nil || math.Abs(r.Window.Start-36.5) > 1e-9 || dbIn(t, r) != 36.5 {
			t.Fatalf("second stroke starts at %+v %v, want +36.5", r, err)
		}
		if r, _ := sup.Write(set(gain3, lvl(t, 37.5), gesture)); dbIn(t, r) != 37.5 || r.State != StateFree {
			t.Fatalf("second stroke: %v %s", dbIn(t, r), r.State)
		}
	})
}
