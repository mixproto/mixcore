// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
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

// Review pass 3, LOW 5: an in-flight value is sent again at most once per
// echo window. A drag held past the cap of a console that does not echo
// sends the cap value once, not every frame.
func TestInFlight_DragPastTheCapWithoutEchoSendsTheCapOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for i := range 125 { // 1 s of 8 ms frames, the finger far past +35.5
			_, _ = sup.Write(set(gain3, lvl(t, math.Min(33+float64(i), 50)), gesture))
			time.Sleep(8 * time.Millisecond)
		}
		caps := 0
		for _, db := range gainWrites(drv) {
			if db == 35.5 {
				caps++
			}
		}
		if caps != 1 {
			t.Fatalf("the cap value was sent %d times in 1 s (writes %v)", caps, gainWrites(drv))
		}
		time.Sleep(2 * time.Second) // one echo window later, still no report
		if r, _ := sup.Write(set(gain3, lvl(t, 50), gesture)); !r.Written {
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

// linearLaw is a test fader law: positions 0..1 for -60..+10 dB.
type linearLaw struct{}

func (linearLaw) PositionFromDB(db float64) float64 { return math.Min(math.Max((db+60)/70, 0), 1) }
func (linearLaw) DBFromPosition(p float64) float64  { return p*70 - 60 }

// tremble rests the finger at the wall for 800 ms (one full sway) with a slow +-amp sway
// (8 ms frames, 2.5 Hz) around the candidate c(0), then asks openWall.
func tremble(t *testing.T, sup *Supervisor, at func(sway float64) float64, amp float64) Result {
	t.Helper()
	for i := range 100 {
		sway := amp * math.Sin(2*math.Pi*2.5*float64(i*8)/1000)
		_, _ = sup.Write(set(ch1Fader, lvl(t, at(sway)), gesture))
		time.Sleep(8 * time.Millisecond)
	}
	r, _ := sup.OpenWall(ch1Fader, gesture)
	return r
}

// Review pass 3, MEDIUM 2: with the track length (landscape 152 pt) a
// resting finger trembling +-3 pt opens the wall: the span is the spec's
// 9 pt (phone-safety.md 4.1), not 0.5 dB (about 1 pt in landscape).
func TestSlipGuard_TrembleAtRestOpensTheWallInLandscape(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: safetyState(-6), caps: gainCaps, law: linearLaw{}}
		sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}},
			Safety: SafetyConfig{Client: ClientSafety{SlipGuard: true}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := sup.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(sup.Stop)
		synctest.Wait()
		const track = 152.0
		_, _ = sup.HoldWindowWith(ch1Fader, gesture, true, HoldOptions{TrackLengthPt: track})
		rest := linearLaw{}.PositionFromDB(0) + 5/track // 5 pt past the wall
		r := tremble(t, sup, func(sway float64) float64 { return linearLaw{}.DBFromPosition(rest + sway/track) }, 3)
		if r.State != StateFree || r.Window.WallUp != 6 {
			t.Fatalf("a resting finger (+-3 pt of 152) did not open the wall: %s %v", r.State, r.Window.WallUp)
		}
	})
}

// Without the track length the span is 2 dB of travel: a +-0.9 dB tremble
// rests (it did not with 0.5 dB); the 12.5 dB/s push stays blocked
// (TestSlipGuard_RestMeansStillness).
func TestSlipGuard_TrembleAtRestOpensTheWallWithoutTrack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		r := tremble(t, sup, func(sway float64) float64 { return 1 + sway }, 0.9)
		if r.State != StateFree {
			t.Fatalf("a +-0.9 dB tremble did not open the wall: %s", r.State)
		}
	})
}

// Review pass 4, MEDIUM 2, probe 1: the re-send dedupe only skips the
// latest value sent. A drag 33.5 -> 34.5 -> 35.5 (cap) -> back to 34.5
// with no echo must send 34.5 again: the console is at 35.5.
func TestInFlight_BackToAnEarlierValueIsSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		var r Result
		for _, c := range []float64{33.5, 34.5, 35.5, 34.5} {
			r, _ = sup.Write(set(gain3, lvl(t, c), gesture))
		}
		if w := gainWrites(drv); !r.Written || len(w) != 4 || w[3] != 34.5 {
			t.Fatalf("back to 34.5: written %v, writes %v", r.Written, w)
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

// Review pass 4, MEDIUM 3: a track length can only make the rest check
// stricter. A finger pushed past the wall at 12.5 dB/s for 0.8 s (to +9
// dB, below the top of the track, where a finger really stops), with the
// UI asking openWall on every frame, opens no wall with a track of 1.0 or
// 0.001 pt (the API refuses those; Go hosts can still pass them), none,
// or 490 pt.
func TestSlipGuard_PushOpensNoWallWhateverTheTrack(t *testing.T) {
	for _, track := range []float64{1.0, 0.001, 0, 490} {
		t.Run(fmt.Sprint(track), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				drv := &fakeDriver{snapshot: safetyState(-6), caps: gainCaps, law: linearLaw{}}
				sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}},
					Safety: SafetyConfig{Client: ClientSafety{SlipGuard: true}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := sup.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(sup.Stop)
				synctest.Wait()
				_, _ = sup.HoldWindowWith(ch1Fader, gesture, true, HoldOptions{TrackLengthPt: track})
				opened, finger, wall := 0, -1.0, 0.0
				for range 100 { // 0.8 s of 8 ms frames, +0.1 dB each
					finger += 0.1
					_, _ = sup.Write(set(ch1Fader, lvl(t, finger), gesture))
					if r, _ := sup.OpenWall(ch1Fader, gesture); r.Window != nil && r.Window.WallUp > wall {
						opened, wall = opened+1, r.Window.WallUp // the wall moved up: it opened
					}
					time.Sleep(8 * time.Millisecond)
				}
				t.Logf("track %v pt: %d walls opened", track, opened)
				if opened != 0 {
					t.Errorf("track %v pt: a pushed finger opened %d walls", track, opened)
				}
			})
		})
	}
}

// Lead decision (review pass 4): a full in-flight set inside a hold is a
// haptic wall, atCap with no write, never ERR_GAIN_BOUND mid-drag; outside
// a hold it stays ERR_GAIN_BOUND (TestInFlight_BurstOfNineIsBounded).
func TestInFlight_FullSetInsideAHoldIsACap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for _, c := range []float64{33, 33.5, 34, 34.5, 35, 35.5, 32, 31.5} { // 8 distinct, no echo
			if _, err := sup.Write(set(gain3, lvl(t, c), gesture)); err != nil {
				t.Fatal(err)
			}
		}
		r, err := sup.Write(set(gain3, lvl(t, 31), gesture)) // a ninth value
		if err != nil || r.State != StateAtCap || r.Written || len(gainWrites(drv)) != 8 {
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
