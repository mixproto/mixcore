// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// SR-28, CR-25: the monitor ceiling must follow only the engineer. These
// are the review findings Go B1 and Sec M1 (hearing safety).

var dca1Fader = model.ParamRef{Strip: model.StripRef{Kind: model.StripDCA, Index: 1}, Field: model.FieldFader}

// dcaFixture is safetyState with one DCA whose master fader the engineer set to -14 dB.
func dcaFixture() model.State {
	st := safetyState(-6).WithGroups(0, 1).WithShape(model.Shape{DCAMasters: true})
	v, _ := model.Level(-14)
	st, err := st.With(dca1Fader, v)
	if err != nil {
		panic(err)
	}
	return st
}

func startSeat(t *testing.T, cfg SafetyConfig, role Role, st model.State) (*Supervisor, *fakeDriver) {
	t.Helper()
	drv := &fakeDriver{snapshot: st, caps: gainCaps}
	sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}},
		Safety: cfg, Role: role})
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

// Go B1: a monitor seat grabbing a DCA fader again and again (its echo
// arriving each time) stays at -14 + 6; the ceiling never climbs by the
// headroom per grab (-14, -8, -2, +4 before the fix).
func TestCeiling_RepeatedHoldsNeverRatchet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seat := SafetyConfig{Client: ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: defaultHeadroomDB}}}
		sup, drv := startSeat(t, seat, Role{}, dcaFixture())
		for grab := range 4 {
			if _, err := sup.HoldWindow(dca1Fader, musician, true); err != nil {
				t.Fatal(err)
			}
			r, err := sup.Write(set(dca1Fader, lvl(t, 10), musician))
			if err != nil || dbIn(t, r) != -8 {
				t.Fatalf("grab %d: %v %+v; want -8 (-14 + 6)", grab+1, dbIn(t, r), err)
			}
			pushChange(t, drv, dca1Fader, lvl(t, -8), musician) // the seat's own echo
			_, _ = sup.HoldWindow(dca1Fader, musician, false)
		}
	})
}

// Sec M1: an echo of the seat's write that is lost and comes back as a
// console change (a late echo, or the background re-read) is not the
// engineer's value: the ceiling stays where the engineer put it.
func TestCeiling_DroppedEchoIsNotCreditedToTheEngineer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -2 {
			t.Fatalf("first raise %v, want -8 + 6", dbIn(t, r))
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -2), desk) // no echo matched: the re-read finds it
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -2 {
			t.Fatalf("the seat's own value became the reference: %v, want -2", dbIn(t, r))
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -6), desk) // the engineer at the desk
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != 0 {
			t.Fatalf("after the engineer's -6: %v, want 0", dbIn(t, r))
		}
	})
}

// Sec M1: the re-read of our own write (OwnWrite) is never the engineer's.
func TestCeiling_OwnWriteRereadIsNotCredited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		drv.session(0).push <- driver.ParamChanged{Param: sendLevel(1, 2), Value: lvl(t, 4), Origin: desk, Authoritative: true, OwnWrite: true}
		synctest.Wait()
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != 4 || r.Written {
			t.Fatalf("own write read back moved the ceiling: %v written %v; want it kept at +4 (above -2, never lowered)", dbIn(t, r), r.Written)
		}
	})
}

// A ceiling parameter the connect did not adopt never falls back to the
// current value (which a seat could have pushed up): it counts from -60 dB.
func TestCeiling_UnknownReferenceIsTheFloorNotTheCurrentValue(t *testing.T) {
	sf := safety{engineer: map[model.ParamRef]float64{}, roles: map[string]Role{}, clients: map[string]ClientSafety{},
		cfg: SafetyConfig{Client: ClientSafety{Monitor: &MonitorCeiling{HeadroomDB: 6}}}}
	if c, ok := sf.ceiling(dca1Fader, "mon-2", 3); !ok || c != ceilingFloorDB+6 {
		t.Fatalf("ceiling %v, %v; want %v", c, ok, ceilingFloorDB+6)
	}
}
