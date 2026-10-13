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

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Review pass 3, HIGH 1 (lead design): per headamp a set of in-flight
// values, every value sent that no report has matched yet. A Gain write is
// bounded against the console value and every one of them.

func pushGain(t *testing.T, drv *fakeDriver, db float64, o driver.Origin, authoritative bool) {
	t.Helper()
	drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, db), Origin: o, Authoritative: authoritative}
	synctest.Wait()
}

var desk = driver.Origin{Kind: driver.OriginConsole}

// assertBounded checks every write moved Gain at most 6 dB from every
// earlier value the console could be at (+32.5 at the start).
func assertBounded(t *testing.T, drv *fakeDriver) {
	t.Helper()
	seen := []float64{32.5}
	for _, w := range drv.session(0).writes() {
		db, _ := w.Value.DB()
		for _, s := range seen {
			if math.Abs(db-s) > gainBoundDB {
				t.Fatalf("write %v is %v dB from %v (writes %v)", db, math.Abs(db-s), s, gainWrites(drv))
			}
		}
		seen = append(seen, db)
	}
}

func gainWrites(drv *fakeDriver) []float64 {
	var out []float64
	for _, w := range drv.session(0).writes() {
		db, _ := w.Value.DB()
		out = append(out, db)
	}
	return out
}

// Probe 1: 30, then 26.5, then the echo of 30, then 36. Clearing on any
// report forgot 26.5 and sent [30 26.5 36], a +9.5 dB step.
func TestInFlight_EchoOfOneValueKeepsTheOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		for _, db := range []float64{30, 26.5} {
			if _, err := sup.Write(set(gain3, lvl(t, db), macro)); err != nil {
				t.Fatal(err)
			}
		}
		pushGain(t, drv, 30, macro, false) // the echo of 30
		if _, err := sup.Write(set(gain3, lvl(t, 36), macro)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("36 with 26.5 still in flight: %v (writes %v)", err, gainWrites(drv))
		}
		assertBounded(t, drv)
	})
}

// Probe 2: a desk push of 32.5 while 26.5 is in flight does not match it,
// so 26.5 stays a reference and 38.5 is refused.
func TestInFlight_NonMatchingReportRemovesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 26.5), macro)); err != nil {
			t.Fatal(err)
		}
		pushGain(t, drv, 32.5, desk, false)
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("38.5 with 26.5 in flight: %v", err)
		}
		assertBounded(t, drv)
	})
}

// An authoritative report (the driver's re-read reply for a parameter with
// unconfirmed writes) clears the whole set: its value is the console's.
func TestInFlight_AuthoritativeReportClearsTheSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		for _, db := range []float64{30, 26.5} {
			_, _ = sup.Write(set(gain3, lvl(t, db), macro))
		}
		pushGain(t, drv, 32.5, desk, true) // neither landed
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); err != nil {
			t.Fatalf("38.5 after the authoritative 32.5: %v", err)
		}
	})
}

// A burst of 9 distinct values without any report: the set holds 8, the
// ninth is refused, and every write stays bounded.
func TestInFlight_BurstOfNineIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		var errs []error
		for i := range 9 {
			_, err := sup.Write(set(gain3, lvl(t, 33+0.5*float64(i)), macro))
			errs = append(errs, err)
		}
		for i, err := range errs[:8] {
			if err != nil {
				t.Fatalf("value %d: %v", i+1, err)
			}
		}
		if !errors.Is(errs[8], ErrGainBound) {
			t.Fatalf("ninth value with 8 in flight: %v", errs[8])
		}
		assertBounded(t, drv)
	})
}

// Stop and Pause end the session: nothing it sent is in flight after a
// new Start.
func TestInFlight_PauseClearsTheSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.Write(set(gain3, lvl(t, 26.5), macro))
		sup.Pause()
		n := inFlightCount(sup)
		if n != 0 {
			t.Fatalf("%d values in flight after Pause", n)
		}
		_ = drv
	})
}

func inFlightCount(sup *Supervisor) int {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	return len(sup.safe.inFlightOf(gain3))
}

// LOW 5 at the range end (v0.5: the cap is the headamp's writeMaxDB,
// CC-2): a drag held past the top of a console that does not echo sends the
// top once per echo window, never a stream of repeats.
func TestInFlight_DragPastTheTopWithoutEchoSendsTheTopOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, gainState(55.5))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for i := range 125 { // 1 s of 8 ms frames, the finger far past +59.5
			_, _ = sup.Write(set(gain3, lvl(t, math.Min(56.5+float64(i), 70)), gesture))
			time.Sleep(8 * time.Millisecond)
		}
		tops := 0
		for _, db := range gainWrites(drv) {
			if db == 59.5 {
				tops++
			}
		}
		if tops != 1 {
			t.Fatalf("the top was sent %d times in 1 s (writes %v)", tops, gainWrites(drv))
		}
		time.Sleep(2 * time.Second) // one echo window later, still no report
		if r, _ := sup.Write(set(gain3, lvl(t, 70), gesture)); !r.Written {
			t.Fatal("after an echo window the value is sent again")
		}
	})
}

// Review pass 3, LOW 4: a report equal to the value the state already has
// (an authoritative re-read of an unchanged Gain, or any repeat) is not an
// external change: it never ends a hold.
func TestHold_ReportOfTheSameValueKeepsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		_ = sup.Drain()
		pushGain(t, drv, 32.5, desk, true)
		if u := sup.Drain(); len(u.Events) != 0 {
			t.Fatalf("events %+v", u.Events)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 33), gesture)); err != nil {
			t.Fatalf("the hold must go on: %v", err)
		}
		pushGain(t, drv, 30, desk, false) // a real desk move still ends it
		if _, err := sup.Write(set(gain3, lvl(t, 31), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("after a desk move: %v", err)
		}
	})
}

// Review pass 3, LOW 3: a grab at +60 dB (read from the console, above the
// writable top +59.5) has its upper cap at the start: holding or pushing
// up writes nothing, never a step down to +59.5.
func TestGain_GrabAboveTheWritableTopNeverWritesDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, _ := stateWith(-6).WithHeadamps([]model.Headamp{{Index: 1}, {Index: 2}, {Index: 3, GainDB: 60}})
		sup, drv := startSafe(t, SafetyConfig{}, st)
		w, _ := sup.HoldWindow(gain3, gesture, true)
		for _, c := range []float64{60, 61, 60} {
			if r, _ := sup.Write(set(gain3, lvl(t, c), gesture)); r.Written || dbIn(t, r) != 60 {
				t.Fatalf("candidate %v: applied %v written %v (cap %v)", c, dbIn(t, r), r.Written, w.Window.CapUp)
			}
		}
		if n := len(drv.session(0).writes()); n != 0 || w.Window.CapUp != 60 {
			t.Fatalf("%d writes, cap %v", n, w.Window.CapUp)
		}
	})
}

// Review pass 4, MEDIUM 2, probe 2: a report since the last send makes the
// same value a real change again. 33.5 sent, the desk moves to 30, a typed
// 33.5 within 2 s is sent.
func TestInFlight_SameValueAfterAReportIsSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.Write(set(gain3, lvl(t, 33.5), gesture))
		pushGain(t, drv, 30, desk, false)
		typed := set(gain3, lvl(t, 33.5), gesture)
		typed.Typed = true
		r, err := sup.Write(typed)
		if err != nil || !r.Written || len(gainWrites(drv)) != 2 {
			t.Fatalf("typed 33.5 after the desk moved to 30: written %v, %v, writes %v", r.Written, err, gainWrites(drv))
		}
	})
}

// Redesign CC-3 (replaces the v0.3 lead decision of review pass 4): inside a
// hold a ninth value in flight is written like any other, never a stop;
// outside a hold it stays ERR_GAIN_BOUND (TestInFlight_BurstOfNineIsBounded).
func TestInFlight_FullSetInsideAHoldNeverStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for _, c := range []float64{33, 33.5, 34, 34.5, 35, 35.5, 32, 31.5} { // 8 distinct, no echo
			if _, err := sup.Write(set(gain3, lvl(t, c), gesture)); err != nil {
				t.Fatal(err)
			}
		}
		r, err := sup.Write(set(gain3, lvl(t, 31), gesture)) // a ninth value
		if err != nil || r.State != StateFree || !r.Written || len(gainWrites(drv)) != 9 {
			t.Fatalf("ninth value in a hold: %v %s written %v, writes %v", err, r.State, r.Written, gainWrites(drv))
		}
	})
}

// Follow-up 2: on a console that does not echo Gain, the authoritative
// re-read of our own write differs from state (it never got the echo). It
// updates the state but never ends the hold of the person dragging (a hold
// that began after the re-read's skip set was taken is not skipped). A
// re-read with another value (the desk) still ends it.
func TestHold_OwnWriteReadBackKeepsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); err != nil {
			t.Fatal(err)
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 33.5), Origin: desk, Authoritative: true, OwnWrite: true}
		synctest.Wait()
		if v, _ := sup.Drain().State.Value(gain3); !v.Equal(lvl(t, 33.5)) {
			t.Fatalf("state %v, want the read-back 33.5", v)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 34), gesture)); err != nil {
			t.Fatalf("the hold must go on after our own write read back: %v", err)
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 30), Origin: desk, Authoritative: true}
		synctest.Wait()
		if _, err := sup.Write(set(gain3, lvl(t, 31), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("after the desk's value: %v", err)
		}
	})
}
