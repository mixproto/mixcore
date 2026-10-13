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

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// Fixture of dybyskz-mix docs/design/acceptance/README.md, reduced: ch 1
// Vox1 at -6 dB, buses 1 Mon 1 Drums, 2 Mon 2 Keys (a linkable pair) and
// 3 Mon 3 Bass; ch 1 sends -10 dB (bus 1) and -8 dB (bus 2), both on.
func busFixture() model.State {
	st, err := safetyState(-6).WithBuses([]model.BusInfo{
		{Index: 1, Kind: model.BusMix, Linkable: true, SendPan: true}, {Index: 2, Kind: model.BusMix, Linkable: true, SendPan: true},
		{Index: 3, Kind: model.BusMix, Linkable: true, SendPan: true},
	})
	if err != nil {
		panic(err)
	}
	st = st.WithShape(model.Shape{Sends: model.SendShape{Level: true, On: true, Pan: true, PairScoped: true}})
	for p, db := range map[model.ParamRef]float64{sendLevel(1, 1): -10, sendLevel(1, 2): -8, busFader(2): -3} {
		v, _ := model.Level(db)
		if st, err = st.With(p, v); err != nil {
			panic(err)
		}
	}
	for _, b := range []int{1, 2} {
		if st, err = st.With(sendOn(1, b), model.Bool(true)); err != nil {
			panic(err)
		}
	}
	return st
}

func sendLevel(input, bus int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: input}, Field: model.FieldSendLevel, Bus: bus}
}

func sendOn(input, bus int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: input}, Field: model.FieldSendOn, Bus: bus}
}

func busFader(b int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: b}, Field: model.FieldFader}
}

func busMute(b int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: b}, Field: model.FieldMute}
}

// monitorOf is a monitor role for the buses, each with the default ceiling.
func monitorOf(buses ...int) Role {
	r := Role{Kind: RoleMonitor}
	for _, b := range buses {
		r.Buses = append(r.Buses, BusGrant{Bus: b, Ceiling: &MonitorCeiling{HeadroomDB: defaultHeadroomDB}})
	}
	return r
}

func startRole(t *testing.T, role Role, st model.State) (*Supervisor, *fakeDriver) {
	t.Helper()
	drv := &fakeDriver{snapshot: st, caps: gainCaps}
	sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}}, Role: role})
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

// AC-MV-03, SR-09, invariant 4: a monitor role writes only sends into its
// buses and those buses' fader and mute; everything else is ErrRole and
// never reaches the console.
func TestRole_MonitorWritesOnlyItsMix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, monitorOf(2), busFixture())
		pan, _ := model.Pan(-20)
		for _, w := range []Write{
			set(sendLevel(1, 2), lvl(t, -9), musician), set(sendOn(1, 2), model.Bool(false), musician),
			set(model.ParamRef{Strip: sendLevel(1, 1).Strip, Field: model.FieldSendPan, Bus: 2}, pan, musician),
			set(busFader(2), lvl(t, -4), musician), set(busMute(2), model.Bool(true), musician),
		} {
			if _, err := sup.Write(w); err != nil && !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("%s: %v", w.Param, err)
			}
		}
		allowed := len(drv.session(0).writes())
		for _, w := range []Write{
			set(sendLevel(1, 1), lvl(t, -20), musician), // the send to Mon 1 Drums (AC-MV-03)
			set(ch1Fader, lvl(t, -20), musician),        // ch 1's channel fader (AC-MV-03)
			set(busFader(1), lvl(t, -20), musician), set(busMute(3), model.Bool(true), musician),
			set(model.ParamRef{Strip: model.MainRef, Field: model.FieldMute}, model.Bool(true), musician),
			set(gain3, lvl(t, 30), musician),
			set(model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: 2}, Field: model.FieldName}, mustText(t, "Mine"), musician),
		} {
			if _, err := sup.Write(w); !errors.Is(err, ErrRole) {
				t.Fatalf("%s: %v, want ErrRole", w.Param, err)
			}
		}
		if err := sup.Toggle(busMute(1), musician); !errors.Is(err, ErrRole) {
			t.Fatalf("toggle of another bus: %v", err)
		}
		if err := sup.Hold(sendLevel(1, 1), musician, true); !errors.Is(err, ErrRole) {
			t.Fatalf("hold of another bus's send: %v", err)
		}
		if n := len(drv.session(0).writes()); n != allowed || allowed != 4 {
			t.Fatalf("console got %d writes, want only the %d allowed ones (pan of an even bus is the pair's)", n, allowed)
		}
	})
}

// A viewer writes nothing; ending a hold and its own slip guard stay allowed.
func TestRole_ViewerWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{Kind: RoleViewer}, busFixture())
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, 0), gesture)); !errors.Is(err, ErrRole) {
			t.Fatalf("set: %v", err)
		}
		if err := sup.Hold(ch1Fader, gesture, true); !errors.Is(err, ErrRole) {
			t.Fatalf("hold: %v", err)
		}
		if err := sup.Hold(ch1Fader, gesture, false); err != nil {
			t.Fatalf("ending a hold is always allowed: %v", err)
		}
		sup.SetSlipGuard("ui", true)
		if err := sup.SetShowModeBy(gesture, true); !errors.Is(err, ErrRole) {
			t.Fatalf("show mode: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}

// Show mode and Gain unlocks are owner and engineer settings.
func TestRole_MonitorCannotChangeShowModeOrUnlockGain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startRole(t, monitorOf(2), busFixture())
		if err := sup.SetShowModeBy(musician, true); !errors.Is(err, ErrRole) {
			t.Fatalf("show mode: %v", err)
		}
		if err := sup.Unlock(gain3, musician); !errors.Is(err, ErrRole) {
			t.Fatalf("unlock: %v", err)
		}
		if err := sup.Relock(gain3, musician); !errors.Is(err, ErrRole) {
			t.Fatalf("relock: %v", err)
		}
		if err := sup.SetShowModeBy(engineerB, true); !errors.Is(err, ErrRole) {
			t.Fatal("every client of this engine is a monitor seat unless the hub says otherwise")
		}
		if err := sup.SetClientRole("foh", Role{Kind: RoleEngineer}); err != nil {
			t.Fatal(err)
		}
		if err := sup.SetShowModeBy(engineerB, true); err != nil {
			t.Fatalf("engineer: %v", err)
		}
	})
}

// roles.md: a role change applies at once; holds the new role does not
// allow end with HoldCancelled reason "role", the others stay.
func TestRole_ChangeEndsHoldsOutsideTheNewMix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		for _, p := range []model.ParamRef{ch1Fader, sendLevel(1, 2)} {
			if err := sup.Hold(p, musician, true); err != nil {
				t.Fatal(err)
			}
		}
		if err := sup.SetClientRole("mon-2", monitorOf(2)); err != nil {
			t.Fatal(err)
		}
		u := sup.Drain()
		if len(u.Events) != 1 || u.Events[0].Kind != HoldCancelled || u.Events[0].Param != ch1Fader || u.Events[0].Reason != ReasonRole {
			t.Fatalf("events %+v", u.Events)
		}
		if _, err := sup.Write(set(ch1Fader, lvl(t, 0), musician)); !errors.Is(err, ErrRole) {
			t.Fatalf("after the change: %v", err)
		}
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, -7), musician)); err != nil {
			t.Fatalf("the send hold stays: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 1 {
			t.Fatalf("%d writes", n)
		}
	})
}

func TestRole_Validation(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Role
		ok   bool
	}{
		{"owner", Role{}, true},
		{"engineer", Role{Kind: RoleEngineer}, true},
		{"monitor of bus 2", monitorOf(2), true},
		{"monitor without buses", Role{Kind: RoleMonitor}, false},
		{"monitor of bus 0", Role{Kind: RoleMonitor, Buses: []BusGrant{{Bus: 0}}}, false},
		{"monitor twice of one bus", monitorOf(2, 2), false},
		{"monitor ceiling above 10 dB", Role{Kind: RoleMonitor, Buses: []BusGrant{{Bus: 1, Ceiling: &MonitorCeiling{HeadroomDB: 11}}}}, false},
		{"monitor ceiling NaN", Role{Kind: RoleMonitor, Buses: []BusGrant{{Bus: 1, Ceiling: &MonitorCeiling{HeadroomDB: math.NaN()}}}}, false},
		{"viewer with buses", Role{Kind: RoleViewer, Buses: []BusGrant{{Bus: 1}}}, false},
		{"unknown kind", Role{Kind: 9}, false},
	} {
		if err := tc.r.Validate(); (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if _, err := New(Config{Driver: &fakeDriver{}, Role: Role{Kind: RoleMonitor}}); err == nil {
		t.Fatal("New must refuse an invalid default role")
	}
	for _, k := range []RoleKind{RoleOwner, RoleEngineer, RoleMonitor, RoleViewer} {
		if got, ok := ParseRoleKind(k.String()); !ok || got != k {
			t.Fatalf("ParseRoleKind(%q) = %v, %v", k.String(), got, ok)
		}
	}
}

func mustText(t *testing.T, s string) model.Value {
	t.Helper()
	v, err := model.Text(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Sec L4: a write is handed to the session while the supervisor still holds
// the lock it checked the role under, so SetClientRole (which takes that
// lock) can never land between the check and the send.
func TestRole_WriteLeavesUnderTheLockOfItsRoleCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		locked := false
		drv.session(0).onApply = func() {
			if sup.mu.TryLock() {
				sup.mu.Unlock()
				return
			}
			locked = true
		}
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, -9), musician)); err != nil {
			t.Fatal(err)
		}
		if !locked {
			t.Fatal("Apply ran after the role check released the lock: a role change could slip in between")
		}
	})
}
