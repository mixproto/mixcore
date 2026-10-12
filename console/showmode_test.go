// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

func undo(t *testing.T, db float64) Write {
	w := set(gain3, lvl(t, db), gesture)
	w.Undo = true
	return w
}

// AC-PS-12, SR-27: show mode locks raising Gain; lowering, faders and
// mutes stay live; turning it on or off writes nothing.
func TestShowMode_LocksGainRaisesOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		sup.SetShowMode(true)
		if u := sup.Drain(); !u.ShowMode || len(drv.session(0).writes()) != 0 {
			t.Fatalf("show mode on: %+v", u.ShowMode)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("raise: %v", err)
		}
		typed := set(gain3, lvl(t, 34), gesture)
		typed.Typed = true
		if _, err := sup.Write(typed); !errors.Is(err, ErrShowMode) {
			t.Fatalf("typed raise: %v", err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 31.5), gesture)); err != nil || lastWrite(drv) != 31.5 {
			t.Fatalf("-1 must stay live: %v", err)
		}
		for _, w := range []Write{set(ch1Fader, lvl(t, 0), gesture), set(model.ParamRef{Strip: ch1Fader.Strip, Field: model.FieldMute}, model.Bool(true), gesture)} {
			if _, err := sup.Write(w); err != nil {
				t.Fatalf("%s must stay live: %v", w.Param, err)
			}
		}
		_, _ = sup.HoldWindow(gain3, gesture, true) // a drag up stops at the start: locked
		if r, _ := sup.Write(set(gain3, lvl(t, 35), gesture)); r.State != StateLocked || r.Written {
			t.Fatalf("drag up in show mode: %v %s", dbIn(t, r), r.State)
		}
	})
}

// AC-PS-13: unlock after the hold, relock on 20 s idle, undo grace 10 s.
func TestShowMode_UnlockIdleRelockAndUndoGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		if err := sup.Unlock(gain3, gesture); err != nil {
			t.Fatal(err)
		}
		if u := sup.Drain(); !slices.Equal(u.Unlocked, []int{3}) {
			t.Fatalf("unlocked %v", u.Unlocked)
		}
		echo := func(db float64) {
			drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, db), Origin: gesture}
			synctest.Wait()
		}
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); err != nil || lastWrite(drv) != 33.5 {
			t.Fatalf("+1 while unlocked: %v", err)
		}
		echo(33.5)
		time.Sleep(19 * time.Second)
		if _, err := sup.Write(set(gain3, lvl(t, 31.5), gesture)); err != nil { // a touch restarts the 20 s
			t.Fatalf("still unlocked: %v", err)
		}
		echo(31.5)
		time.Sleep(20*time.Second + time.Millisecond)
		synctest.Wait()
		u := sup.Drain()
		if len(u.Unlocked) != 0 || !slices.Equal(u.UnlockEnded, []UnlockEvent{{Headamp: 3, Reason: UnlockIdle}}) {
			t.Fatalf("after 20 s idle: %+v %+v", u.Unlocked, u.UnlockEnded)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 35.5), gesture)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("raise after relock: %v", err)
		}
		if _, err := sup.Write(undo(t, 33.5)); err != nil || lastWrite(drv) != 33.5 { // the value before the second entry
			t.Fatalf("undo within 10 s of the relock: %v", err)
		}
		time.Sleep(11 * time.Second)
		if _, err := sup.Write(undo(t, 33.5)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("undo after 10 s: %v", err)
		}
	})
}

// AC-PS-13, AC-PS-14: relock and stale end the unlock; show mode survives
// the reconnect; outside show mode unlock does nothing.
func TestShowMode_RelockStaleAndReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		_ = sup.Unlock(gain3, gesture)
		_ = sup.Relock(gain3, gesture)
		if u := sup.Drain(); len(u.UnlockEnded) != 1 || u.UnlockEnded[0].Reason != UnlockRelock {
			t.Fatalf("relock: %+v", u.UnlockEnded)
		}
		_ = sup.Unlock(gain3, gesture)
		drv.session(0).lose()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		u := sup.Drain()
		if !u.ShowMode || len(u.Unlocked) != 0 || u.Status != StatusLive {
			t.Fatalf("after reconnect: show %t unlocked %v status %s", u.ShowMode, u.Unlocked, u.Status)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("raise after stale: %v", err)
		}
		sup.SetShowMode(false)
		if err := sup.Unlock(gain3, gesture); err != nil || len(sup.Drain().Unlocked) != 0 {
			t.Fatal("unlock outside show mode does nothing")
		}
		if err := sup.Unlock(ch1Fader, gesture); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("unlock of a fader: %v", err)
		}
	})
}

// AC-PS-15, SR-28: the monitor ceiling is the engineer's latest value +
// headroom, follows the console and other engineer devices, never the
// musician's own writes, and never falls below the engineer's value.
func TestMonitorCeiling_FollowsTheEngineer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(2))
		sup.SetClientSafety("mon-2", ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: defaultHeadroomDB}})
		push := func(v float64, o driver.Origin) {
			drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, v), Origin: o}
			synctest.Wait()
		}
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 10), musician)); dbIn(t, r) != 8 || r.State != StateAtCeiling {
			t.Fatalf("+1 x8 stops at +8: %v %s", dbIn(t, r), r.State)
		}
		push(8, musician) // the echo of the musician's own write
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 10), musician)); dbIn(t, r) != 8 {
			t.Fatalf("own echo moved the reference: %v", dbIn(t, r))
		}
		push(-4, driver.Origin{Kind: driver.OriginConsole}) // the engineer at the console
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 10), musician)); dbIn(t, r) != 2 {
			t.Fatalf("ceiling after the console moved it to -4: %v", dbIn(t, r))
		}
		push(1, engineerB) // an engineer device through the same core
		sup.SetClientSafety("mon-2", ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: 0}})
		_, _ = sup.HoldWindow(ch1Fader, musician, true)
		r, _ := sup.Write(set(ch1Fader, lvl(t, 9), musician))
		if dbIn(t, r) != 1 || r.Window == nil || r.Window.Ceiling != 1 || r.Window.OpensByRest {
			t.Fatalf("headroom 0: %v %+v", dbIn(t, r), r.Window)
		}
		if _, err := sup.OpenWall(ch1Fader, musician); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("a ceiling never opens: %v", err)
		}
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 9), engineerB)); dbIn(t, r) != 9 {
			t.Fatal("an engineer device has no ceiling")
		}
	})
}

// Review LOW 8, contract rule 7: the ceiling covers every fader (here the
// main fader the engineer set to -10 dB: a raise stops at -4), and only
// faders: Gain is bounded by CR-23, not by the ceiling. Sends are not
// modelled yet.
func TestMonitorCeiling_CoversFadersOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(2))
		sup.SetClientSafety("mon-2", ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: defaultHeadroomDB}})
		mainFader := model.ParamRef{Strip: model.MainRef, Field: model.FieldFader}
		drv.session(0).push <- driver.ParamChanged{Param: mainFader, Value: lvl(t, -10), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		if r, _ := sup.Write(set(mainFader, lvl(t, 0), musician)); dbIn(t, r) != -4 || r.State != StateAtCeiling {
			t.Fatalf("main fader: %v %s, want -4 atCeiling", dbIn(t, r), r.State)
		}
		if r, err := sup.Write(set(gain3, lvl(t, 37.5), musician)); err != nil || dbIn(t, r) != 37.5 || r.State != StateFree {
			t.Fatalf("Gain has no ceiling: %v %s %v", dbIn(t, r), r.State, err)
		}
	})
}

// SR-28: we never lower a value to meet the ceiling.
func TestMonitorCeiling_NeverLowersAValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{Client: ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: 6}}}, safetyState(-6))
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, 5), Origin: musician}
		synctest.Wait()
		sup.SetClientSafety("mon-2", ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: 0}}) // ceiling -6 < value +5
		r, err := sup.Write(set(ch1Fader, lvl(t, 7), musician))
		if err != nil || dbIn(t, r) != 5 || r.Written {
			t.Fatalf("raise above the ceiling from +5: %v written %t %v", dbIn(t, r), r.Written, err)
		}
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 3), musician)); dbIn(t, r) != 3 || !r.Written {
			t.Fatal("moving down stays free")
		}
	})
}

// An entry made during an unlock keeps its undo grace even when the write
// left before the echo of the previous one: the value it undoes to is the
// one in flight, not the stale console value.
func TestShowMode_UndoGraceWithoutEcho(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		_ = sup.Unlock(gain3, gesture)
		for _, db := range []float64{30.5, 28.5} { // the second leaves before the echo of the first
			if _, err := sup.Write(set(gain3, lvl(t, db), gesture)); err != nil {
				t.Fatal(err)
			}
		}
		_ = sup.Relock(gain3, gesture)
		if _, err := sup.Write(undo(t, 30.5)); err != nil {
			t.Fatalf("undo within the grace: %v", err)
		}
	})
}

// Review LOW 7: the undo grace after a relock only returns to a value Gain
// had before an entry of that unlock; any other raise flagged undo is show
// mode's refusal.
func TestShowMode_UndoGraceOnlyToValuesBeforeTheEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		_ = sup.Unlock(gain3, gesture)
		if _, err := sup.Write(set(gain3, lvl(t, 30.5), gesture)); err != nil {
			t.Fatal(err)
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 30.5), Origin: gesture} // the echo
		synctest.Wait()
		_ = sup.Relock(gain3, gesture)
		for _, db := range []float64{36.5, 33} {
			if _, err := sup.Write(undo(t, db)); !errors.Is(err, ErrShowMode) {
				t.Fatalf("undo to %v, never a value before an entry: %v", db, err)
			}
		}
		if _, err := sup.Write(undo(t, 32.5)); err != nil || lastWrite(drv) != 32.5 {
			t.Fatalf("undo to the value before the entry: %v", err)
		}
	})
}

// Review MEDIUM 3: a held fader that stands above its ceiling (headroom
// lowered after the musician raised it) never goes down, holding still or
// on a small upward move; the window ceiling is never below the start.
func TestMonitorCeiling_HeldAboveTheCeilingNeverLowers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(2))
		sup.SetClientSafety("mon-2", ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: 10}})
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, 9), Origin: musician} // its own echo
		synctest.Wait()
		sup.SetClientSafety("mon-2", ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: 0}}) // ceiling 2 < value 9
		r, _ := sup.HoldWindow(ch1Fader, musician, true)
		if r.Window.Ceiling != 9 {
			t.Fatalf("window ceiling %v, want the start 9", r.Window.Ceiling)
		}
		for _, cand := range []float64{9, 9.1, 10} {
			r, err := sup.Write(set(ch1Fader, lvl(t, cand), musician))
			if err != nil || dbIn(t, r) != 9 || r.Written {
				t.Fatalf("candidate %v -> %v written %t (%v): a held value above the ceiling must stay", cand, dbIn(t, r), r.Written, err)
			}
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
		if r, _ := sup.Write(set(ch1Fader, lvl(t, 7), musician)); dbIn(t, r) != 7 {
			t.Fatal("moving down stays free")
		}
	})
}
