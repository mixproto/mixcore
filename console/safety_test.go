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
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// Fixture of docs/design/acceptance/phone-safety.md: Vox1 fader -6.0 dB,
// headamp 3 Gain +32.5 dB, range -12..+60 dB in 0.5 dB steps.
var (
	gain3     = model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: 3}, Field: model.FieldGain}
	gainCaps  = model.Capabilities{Headamps: 3, HeadampMinDB: -12, HeadampMaxDB: 60, HeadampStepDB: 0.5, InputCount: 1, HasMain: true}
	macro     = driver.Origin{Kind: driver.OriginMacro, ClientID: "ui", Seq: 1}
	musician  = driver.Origin{Kind: driver.OriginGesture, ClientID: "mon-2", Seq: 1}
	engineerB = driver.Origin{Kind: driver.OriginGesture, ClientID: "foh", Seq: 1}
)

func safetyState(faderDB float64) model.State {
	return stateWith(faderDB).WithHeadamps([]float64{0, 0, 32.5})
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
		}{{33.5, 33.5, StateFree}, {60, 35.5, StateAtCap}, {0, 26.5, StateAtCap}} {
			if r, _ := sup.Write(set(gain3, lvl(t, tc.cand), gesture)); dbIn(t, r) != tc.want || r.State != tc.st {
				t.Fatalf("candidate %v: %v %s", tc.cand, dbIn(t, r), r.State)
			}
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
