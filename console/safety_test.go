// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/backoff"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// Fixture of docs/design/acceptance/phone-safety.md: Vox1 fader -6.0 dB,
// headamp 3 Gain +32.5 dB, range -12..+60 dB in 0.5 dB steps.
var (
	gain3     = model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: 3}, Field: model.FieldGain}
	gainCaps  = model.Capabilities{Headamps: micHeadamps(3), InputCount: 1, HasMain: true}
	macro     = driver.Origin{Kind: driver.OriginMacro, ClientID: "ui", Seq: 1}
	musician  = driver.Origin{Kind: driver.OriginGesture, ClientID: "mon-2", Seq: 1}
	engineerB = driver.Origin{Kind: driver.OriginGesture, ClientID: "foh", Seq: 1}
)

func safetyState(faderDB float64) model.State {
	st, err := stateWith(faderDB).WithHeadamps([]model.Headamp{{Index: 1}, {Index: 2}, {Index: 3, GainDB: 32.5}})
	if err != nil {
		panic(err)
	}
	return st
}

// micHeadamps describes headamps 1..n with the mic law (-12..+60 dB, 0.5 dB).
func micHeadamps(n int) []model.HeadampInfo {
	out := make([]model.HeadampInfo, n)
	for i := range out {
		out[i] = model.HeadampInfo{Index: i + 1, Bank: model.BankLocal, Number: i + 1, MinDB: -12, MaxDB: 60, WriteMaxDB: 59.5, StepDB: 0.5, Writable: true}
	}
	return out
}

func startSafe(t *testing.T, cfg SafetyConfig, st model.State) (*Supervisor, *fakeDriver) {
	t.Helper()
	drv := &fakeDriver{snapshot: st, caps: gainCaps}
	sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}}, Safety: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Stop)
	synctest.Wait()
	return sup, drv
}

func set(p model.ParamRef, v model.Value, o driver.Origin) Write {
	return Write{Command: driver.Command{Param: p, Value: v, Origin: o}}
}

func dbIn(t *testing.T, r Result) float64 {
	t.Helper()
	db, _ := r.Value.DB()
	return db
}

// lastWrite returns the value of the last write the console got (-1000: none).
func lastWrite(drv *fakeDriver) float64 {
	w := drv.session(len(drv.sessions) - 1).writes()
	if len(w) == 0 {
		return -1000
	}
	db, _ := w[len(w)-1].Value.DB()
	return db
}

// AC-PS-01 core part, SR-26: one gesture stops 6 dB above its start; down is
// never stopped; nothing is written above the wall.
func TestSlipGuard_StopsSixDBUpNeverDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		r, err := sup.HoldWindow(ch1Fader, gesture, true)
		if err != nil || r.Window == nil || !r.Window.HasWall || r.Window.WallUp != 0 || !r.Window.OpensByRest {
			t.Fatalf("window %+v, %v", r.Window, err)
		}
		for _, tc := range []struct {
			cand, want float64
			st         GestureState
		}{{-3, -3, StateFree}, {4, 0, StateAtWall}, {10, 0, StateAtWall}, {-1, -1, StateFree}, {-40, -40, StateFree}} {
			r, err := sup.Write(set(ch1Fader, lvl(t, tc.cand), gesture))
			if err != nil || dbIn(t, r) != tc.want || r.State != tc.st {
				t.Fatalf("candidate %v: %v %s, %v", tc.cand, dbIn(t, r), r.State, err)
			}
		}
		for _, w := range drv.session(0).writes() {
			if db, _ := w.Value.DB(); db > 0 {
				t.Fatalf("wrote %v dB above the wall", db)
			}
		}
	})
}

// AC-PS-08: from -inf the floor makes the first stop -10 dB.
func TestSlipGuard_FloorFromMinusInf(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(math.Inf(-1)))
		r, _ := sup.HoldWindow(ch1Fader, gesture, true)
		if r.Window.WallUp != -10 {
			t.Fatalf("wall %v", r.Window.WallUp)
		}
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 10), gesture)); dbIn(t, r) != -10 {
			t.Fatalf("P2 from -inf ends at %v", dbIn(t, r))
		}
	})
}

// AC-PS-07: after a rest at the wall the value continues from the wall
// without a jump, and the next stop is 6 dB higher.
func TestSlipGuard_OpenWallContinuesWithoutJump(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		if r, _ := sup.OpenWall(ch1Fader, gesture); r.State != StateFree || r.Window.WallUp != 0 {
			t.Fatal("a wall opens only while the value stands at it")
		}
		_, _ = sup.Write(set(ch1Fader, lvl(t, 3), gesture)) // the finger is 3 dB past the wall
		time.Sleep(300 * time.Millisecond)                  // wall.dwell
		r, err := sup.OpenWall(ch1Fader, gesture)
		if err != nil || r.Window.WallUp != 6 {
			t.Fatalf("open: %+v %v", r.Window, err)
		}
		for _, tc := range []struct{ cand, want float64 }{{3, 0}, {3.5, 0.5}, {8, 5}, {12, 6}, {2, -1}} {
			if r, _ := sup.Write(set(ch1Fader, lvl(t, tc.cand), gesture)); math.Abs(dbIn(t, r)-tc.want) > 1e-9 {
				t.Fatalf("candidate %v -> %v, want %v (no jump)", tc.cand, dbIn(t, r), tc.want)
			}
		}
	})
}

// AC-PS-09: the slip guard is a per-client setting; off moves freely.
func TestSlipGuard_PerClientSetting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		sup.SetShowMode(true) // show mode never changes how faders feel
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 8), gesture)); dbIn(t, r) != 8 || r.State != StateFree {
			t.Fatalf("slip guard off: %v %s", dbIn(t, r), r.State)
		}
		sup.SetSlipGuard("ui", true)
		_, _ = sup.HoldWindow(ch1Fader, gesture, false)
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 20), gesture)); dbIn(t, r) != 0 {
			t.Fatalf("slip guard on: %v", dbIn(t, r))
		}
	})
}

// AC-PS-18: after stale no wall opens and the gesture stays ended.
func TestSlipGuard_StaleEndsTheGestureAtTheWall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		_, _ = sup.Write(set(ch1Fader, lvl(t, 4), gesture))
		n := len(drv.session(0).writes())
		drv.session(0).lose()
		synctest.Wait()
		time.Sleep(time.Second) // the link returns
		synctest.Wait()
		if _, err := sup.OpenWall(ch1Fader, gesture); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("open wall after stale: %v", err)
		}
		if _, err := sup.Write(set(ch1Fader, lvl(t, 4), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("write after stale: %v", err)
		}
		if got := len(drv.session(len(drv.sessions) - 1).writes()); len(drv.sessions) < 2 || got != 0 || n != 1 {
			t.Fatalf("writes after the reconnect %d (sessions %d, before %d)", got, len(drv.sessions), n)
		}
	})
}

// AC-PS-03/04 core part, SR-24: a Gain gesture stays in +3 / -6 dB and the
// cap never opens.
func TestGain_GestureCaps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		r, err := sup.HoldWindow(gain3, gesture, true)
		if err != nil || !r.Window.HasCap || r.Window.CapUp != 35.5 || r.Window.CapDown != 26.5 || r.Window.OpensByRest {
			t.Fatalf("window %+v %v", r.Window, err)
		}
		for _, tc := range []struct {
			cand, want float64
			st         GestureState
		}{{33.5, 33.5, StateFree}, {60, 35.5, StateAtCap}, {0, 29.5, StateAtCap}, {0, 26.5, StateAtCap}} {
			// From +35.5 a pull to 0 first stops 6 dB lower (the per-write bound,
			// review HIGH 1), then at the -6 dB cap of the gesture. Each write is
			// echoed, as by a console that echoes, so it leaves the in-flight set.
			r, _ := sup.Write(set(gain3, lvl(t, tc.cand), gesture))
			if dbIn(t, r) != tc.want || r.State != tc.st {
				t.Fatalf("candidate %v: %v %s", tc.cand, dbIn(t, r), r.State)
			}
			drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, tc.want), Origin: gesture}
			synctest.Wait()
		}
		if _, err := sup.OpenWall(gain3, gesture); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("a Gain cap must never open: %v", err)
		}
		if max := maxWrite(drv); max > 35.5 {
			t.Fatalf("wrote %v", max)
		}
	})
}

func maxWrite(drv *fakeDriver) float64 {
	m := math.Inf(-1)
	for _, w := range drv.session(len(drv.sessions) - 1).writes() {
		db, _ := w.Value.DB()
		m = math.Max(m, db)
	}
	return m
}

// AC-PS-19, SR-31: every Gain write path is bounded to 6 dB from the console
// value; only a typed entry may move further, after confirmation.
func TestGain_PerWriteBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		for name, w := range map[string]Write{
			"gesture": set(gain3, lvl(t, 45), gesture), "macro": set(gain3, lvl(t, 45), macro),
			"undo":         {Command: driver.Command{Param: gain3, Value: lvl(t, 45), Origin: gesture}, Undo: true},
			"other device": set(gain3, lvl(t, 45), engineerB), "down": set(gain3, lvl(t, 20), gesture),
		} {
			if _, err := sup.Write(w); !errors.Is(err, ErrGainBound) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes past the bound", n)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); err != nil || lastWrite(drv) != 38.5 {
			t.Fatalf("6.0 dB must pass: %v", err)
		}
		typed := Write{Command: driver.Command{Param: gain3, Value: lvl(t, 45), Origin: gesture}, Typed: true}
		if _, err := sup.Write(typed); !errors.Is(err, ErrNeedsConfirm) {
			t.Fatalf("typed +12.5: %v", err)
		}
		typed.Confirmed = true
		if _, err := sup.Write(typed); err != nil || lastWrite(drv) != 45 {
			t.Fatalf("typed and confirmed: %v", err)
		}
	})
}

// AC-PS-16, SR-30: typed raises over 6 dB ask; lowering and small raises do not.
func TestTyped_BigRaiseNeedsConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct {
			from, to float64
			ask      bool
		}{{-6, -10, false}, {-6, -2, false}, {-20, 0, true}, {math.Inf(-1), -30, true}, {-6, 0, false}} {
			sup, drv := startSafe(t, SafetyConfig{}, safetyState(tc.from))
			w := Write{Command: driver.Command{Param: ch1Fader, Value: lvl(t, tc.to), Origin: gesture}, Typed: true}
			_, err := sup.Write(w)
			if errors.Is(err, ErrNeedsConfirm) != tc.ask || (tc.ask && lastWrite(drv) != -1000) {
				t.Fatalf("%v -> %v: %v", tc.from, tc.to, err)
			}
			sup.Stop()
		}
	})
}

// Review HIGH 1: the Gain bound holds against the value still in flight,
// not only the echoed one. Gain 32.5: a macro writes 26.5, then 38.5 before
// any echo; the second would move the console +12 dB in one write.
func TestGain_BoundAgainstThePendingValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 26.5), macro)); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("38.5 after a pending 26.5: %v", err)
		}
		if lastWrite(drv) != 26.5 {
			t.Fatalf("last write %v", lastWrite(drv))
		}
		time.Sleep(3 * time.Second) // no echo: the value in flight stays the reference (re-review MEDIUM B)
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("38.5 3 s after an unechoed 26.5: %v", err)
		}
		_, _ = sup.HoldWindow(gain3, gesture, true) // inside a window the pending value clamps too
		if r, _ := sup.Write(set(gain3, lvl(t, 35.5), gesture)); dbIn(t, r) > 32.5 {
			t.Fatalf("drag up with 26.5 pending applied %v, more than 6 dB above it", dbIn(t, r))
		}
		_, _ = sup.HoldWindow(gain3, gesture, false)
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 32.5), Origin: driver.Origin{Kind: driver.OriginConsole}, Authoritative: true}
		synctest.Wait() // the re-read reports 32.5: the 26.5 never landed
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); err != nil {
			t.Fatalf("after the console's report only its value counts: %v", err)
		}
	})
}

// Re-review MEDIUM B: Gain echo is unverified (X32-F79, XA-F116), so the
// value in flight never expires on a timer. The probe without echo: 38.5,
// 2.1 s, 26.5, 2.1 s, 38.5 sent all three, a +12 dB step at the console.
func TestGain_InFlightBoundHoldsWithoutEcho(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		var errs []error
		for i, db := range []float64{38.5, 26.5, 38.5} {
			if i > 0 {
				time.Sleep(2100 * time.Millisecond)
			}
			_, err := sup.Write(set(gain3, lvl(t, db), macro))
			errs = append(errs, err)
		}
		if errs[0] != nil || !errors.Is(errs[1], ErrGainBound) || errs[2] != nil {
			t.Fatalf("errors %v, want 26.5 refused", errs)
		}
		prev := 32.5
		for _, w := range drv.session(0).writes() {
			db, _ := w.Value.DB()
			if math.Abs(db-prev) > gainBoundDB {
				t.Fatalf("a write moved Gain %v -> %v", prev, db)
			}
			prev = db
		}
	})
}

// Re-review MEDIUM B with echo: the echo ends the value it matches, an
// authoritative re-read ends them all and its value is the reference from
// then on; a tap of the value in flight while the console still reports
// another is sent again, never skipped as a no-op.
func TestGain_ConsoleReportEndsTheInFlightValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		push := func(db float64, o driver.Origin) {
			drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, db), Origin: o}
			synctest.Wait()
		}
		_, _ = sup.Write(set(gain3, lvl(t, 38.5), gesture))
		if r, err := sup.Write(set(gain3, lvl(t, 38.5), gesture)); err != nil || !r.Written {
			t.Fatalf("38.5 again before any report: written %v, %v", r.Written, err)
		}
		push(38.5, gesture) // the echo
		if _, err := sup.Write(set(gain3, lvl(t, 44.5), gesture)); err != nil {
			t.Fatalf("+6 from the echoed 38.5: %v", err)
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 38.5), Origin: driver.Origin{Kind: driver.OriginConsole}, Authoritative: true}
		synctest.Wait() // the re-read says 38.5: 44.5 never landed
		if _, err := sup.Write(set(gain3, lvl(t, 32.5), gesture)); err != nil {
			t.Fatalf("-6 from the reported 38.5: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 4 {
			t.Fatalf("%d writes, want 4", n)
		}
	})
}

// Review HIGH 1, show mode: a pending lowering followed by a raise back to
// the console value is a raise of the console.
func TestShowMode_RaiseAfterPendingLowering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 31.5), gesture)); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 32.5), gesture)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("raise back to 32.5 over a pending 31.5: %v", err)
		}
	})
}

// Review HIGH 2: each opened wall continues from that wall; opening a second
// (and third) wall must not count the first offset twice (a jump down).
func TestSlipGuard_OpenSeveralWallsWithoutJump(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		prev := math.Inf(-1)
		push := func(cand, want float64, st GestureState) {
			t.Helper()
			r, err := sup.Write(set(ch1Fader, lvl(t, cand), gesture))
			if err != nil || math.Abs(dbIn(t, r)-want) > 1e-9 || r.State != st {
				t.Fatalf("candidate %v -> %v %s (%v), want %v %s", cand, dbIn(t, r), r.State, err, want, st)
			}
			if dbIn(t, r) < prev {
				t.Fatalf("value fell from %v to %v while the finger only moved up", prev, dbIn(t, r))
			}
			prev = dbIn(t, r)
		}
		open := func(wantWall float64) {
			t.Helper()
			time.Sleep(300 * time.Millisecond) // wall.dwell
			if r, err := sup.OpenWall(ch1Fader, gesture); err != nil || r.Window.WallUp != wantWall {
				t.Fatalf("open: wall %v, %v", r.Window.WallUp, err)
			}
		}
		push(4, 0, StateAtWall) // finger 4 dB past the first wall
		open(6)
		push(4, 0, StateFree) // resting finger: no jump
		push(12, 6, StateAtWall)
		open(12)
		push(12, 6, StateFree) // the reviewer's probe wrote +2 here
		push(13, 7, StateFree)
		push(20, 12, StateAtWall)
		open(18)
		push(20, 12, StateFree) // third wall
		push(21, 13, StateFree)
		if max := maxWrite(drv); max != 13 {
			t.Fatalf("highest write %v", max)
		}
	})
}

// Review MEDIUM 4: the core refuses openWall until the value has stood at
// the wall for wall.dwell (300 ms), so a buggy UI cannot turn the guard off.
func TestSlipGuard_OpenWallNeedsTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		_, _ = sup.Write(set(ch1Fader, lvl(t, 3), gesture))
		for _, wait := range []time.Duration{0, 299 * time.Millisecond} {
			time.Sleep(wait)
			r, err := sup.OpenWall(ch1Fader, gesture)
			if err != nil || r.State != StateAtWall || r.Window.WallUp != 0 {
				t.Fatalf("open after %s: %s wall %v %v; must stay at the wall", wait, r.State, r.Window.WallUp, err)
			}
		}
		_, _ = sup.Write(set(ch1Fader, lvl(t, 3.3), gesture)) // a tremble within the span: the rest keeps counting
		time.Sleep(time.Millisecond)
		if r, _ := sup.OpenWall(ch1Fader, gesture); r.State != StateFree || r.Window.WallUp != 6 {
			t.Fatalf("open after 300 ms: %s wall %v", r.State, r.Window.WallUp)
		}
		_, _ = sup.Write(set(ch1Fader, lvl(t, 10), gesture)) // the next wall
		_, _ = sup.Write(set(ch1Fader, lvl(t, 0), gesture))  // leaves the wall
		_, _ = sup.Write(set(ch1Fader, lvl(t, 10), gesture)) // back at the wall: a new rest starts
		time.Sleep(200 * time.Millisecond)
		if r, _ := sup.OpenWall(ch1Fader, gesture); r.State != StateAtWall {
			t.Fatal("a new arrival needs a new 300 ms rest")
		}
	})
}

// Review MEDIUM 5: only a person types. A macro (or any non-gesture origin)
// claiming typed would skip the Gain bound SR-31 puts on macros.
func TestTyped_OnlyFromAGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		w := set(gain3, lvl(t, 45), macro)
		w.Typed, w.Confirmed = true, true
		if _, err := sup.Write(w); !errors.Is(err, ErrTypedOrigin) {
			t.Fatalf("typed macro: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}

// x32: a patch change brings in an AES50 headamp mid-session. Its first
// value makes the next update a snapshot, so the UI gets the headamp's
// law and label with it (state.schema.json headamps).
func TestHeadamp_NewHeadampMakesTheNextUpdateASnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Drain()
		gain4 := model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: 4}, Field: model.FieldGain}
		drv.session(0).push <- driver.ParamChanged{Param: gain4, Value: lvl(t, 20), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		u := sup.Drain()
		if !u.Full || len(u.State.Headamps()) != 4 {
			t.Fatalf("full %v headamps %+v", u.Full, u.State.Headamps())
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain4, Value: lvl(t, 21), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		if u := sup.Drain(); u.Full || len(u.Changes) != 1 {
			t.Fatalf("a known headamp is a diff: full %v changes %d", u.Full, len(u.Changes))
		}
	})
}

// Re-review MEDIUM A: the value in flight belongs to the session that sent
// it. A write of 33.5 lost with its session, then a reconnect that reads
// 32.5: tapping 33.5 again is sent (it was a no-op with written false).
func TestGain_InFlightValueDroppedOnStaleAndReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); err != nil {
			t.Fatal(err)
		}
		drv.setDown(true)
		drv.session(0).lose() // before the echo
		synctest.Wait()
		drv.setDown(false)
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		r, err := sup.Write(set(gain3, lvl(t, 33.5), gesture))
		if s := drv.session(1); err != nil || !r.Written || s == nil || len(s.writes()) != 1 {
			t.Fatalf("33.5 after the reconnect: written %v, %v", r.Written, err)
		}
	})
}

// Re-review MEDIUM A: a write the session failed to send is not in flight.
func TestGain_FailedApplyIsNotInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		sendErr := errors.New("send failed")
		drv.session(0).failApplies(sendErr)
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); !errors.Is(err, sendErr) {
			t.Fatalf("failed send: %v", err)
		}
		drv.session(0).failApplies(nil)
		r, err := sup.Write(set(gain3, lvl(t, 33.5), gesture))
		if err != nil || !r.Written || len(drv.session(0).writes()) != 1 {
			t.Fatalf("33.5 again: written %v, %v", r.Written, err)
		}
	})
}

// Re-review LOW C: the rest is stillness, not time at the wall. A UI that
// keeps pushing the finger up past the wall (12.5 dB/s in the probe) never
// opens it; once the finger stays within 0.5 dB of fader travel for 300 ms
// it opens.
func TestSlipGuard_RestMeansStillness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{Client: ClientSafety{SlipGuard: true}}, safetyState(-6))
		_, _ = sup.HoldWindow(ch1Fader, gesture, true)
		finger := 0.0
		for range 50 { // 400 ms at 12.5 dB/s, 0.1 dB per 8 ms frame, all past the wall
			finger += 0.1
			_, _ = sup.Write(set(ch1Fader, lvl(t, finger), gesture))
			time.Sleep(8 * time.Millisecond)
		}
		if r, _ := sup.OpenWall(ch1Fader, gesture); r.State != StateAtWall || r.Window.WallUp != 0 {
			t.Fatalf("a moving finger opened the wall: %s %v", r.State, r.Window.WallUp)
		}
		time.Sleep(300 * time.Millisecond) // now still
		if r, _ := sup.OpenWall(ch1Fader, gesture); r.State != StateFree || r.Window.WallUp != 6 {
			t.Fatalf("after 300 ms still: %s %v", r.State, r.Window.WallUp)
		}
	})
}

// Re-review LOW D: the Gain cap of a gesture stops at the headamp's
// writable top (+59.5 dB), never at the wire maximum the driver refuses.
func TestGain_CapStopsAtTheWritableTop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, _ := stateWith(-6).WithHeadamps([]model.Headamp{{Index: 1}, {Index: 2}, {Index: 3, GainDB: 58}})
		sup, drv := startSafe(t, SafetyConfig{}, st)
		w, _ := sup.HoldWindow(gain3, gesture, true)
		r, _ := sup.Write(set(gain3, lvl(t, 61), gesture))
		if w.Window.CapUp != 59.5 || dbIn(t, r) != 59.5 || r.State != StateAtCap || lastWrite(drv) != 59.5 {
			t.Fatalf("cap %v, applied %v %s", w.Window.CapUp, dbIn(t, r), r.State)
		}
	})
}
