// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

func pushChange(t *testing.T, drv *fakeDriver, p model.ParamRef, v model.Value, o driver.Origin) {
	t.Helper()
	drv.session(len(drv.sessions) - 1).push <- driver.ParamChanged{Param: p, Value: v, Origin: o}
	synctest.Wait()
}

// AC-SF-03, SR-01: a send is held and written like a fader; a change at
// the desk ends the gesture (SR-15) and further sets are ErrGestureEnded.
func TestSend_HoldWindowAndExternalChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		r, err := sup.HoldWindow(sendLevel(1, 2), gesture, true)
		if err != nil || r.Window == nil || dbIn(t, r) != -8 {
			t.Fatalf("hold: %+v, %v", r, err)
		}
		if r, err := sup.Write(set(sendLevel(1, 2), lvl(t, -6), gesture)); err != nil || !r.Written || dbIn(t, r) != -6 {
			t.Fatalf("set: %+v, %v", r, err)
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -6), gesture) // our echo never ends the gesture
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, -5), gesture)); err != nil {
			t.Fatalf("after our echo: %v", err)
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -20), desk)
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, -4), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("after a desk change: %v", err)
		}
		u := sup.Drain()
		if len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange || u.Events[0].Param != sendLevel(1, 2) {
			t.Fatalf("events %+v", u.Events)
		}
		if w := drv.session(0).writes(); len(w) != 2 || w[0].Param != sendLevel(1, 2) {
			t.Fatalf("writes %+v", w)
		}
	})
}

// SR-26: the slip guard (when a client turns it on) works on a send like on a fader.
func TestSend_SlipGuardWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startRole(t, Role{}, busFixture())
		sup.SetSlipGuard("ui", true)
		r, _ := sup.HoldWindow(sendLevel(1, 2), gesture, true)
		if !r.Window.HasWall || r.Window.WallUp != -2 {
			t.Fatalf("window %+v", r.Window)
		}
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 6), gesture)); dbIn(t, r) != -2 || r.State != StateAtWall {
			t.Fatalf("stopped at %v %s", dbIn(t, r), r.State)
		}
	})
}

// AC-MV-05, SR-28, CR-25: a monitor seat's send stops at the engineer's
// latest value of that send + the bus headroom; its own echoes never move
// the reference; the desk does; "off" means no ceiling; never lowered.
func TestSend_MonitorCeilingPerBus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		role := Role{Kind: RoleMonitor, Buses: []BusGrant{{Bus: 2, Ceiling: &MonitorCeiling{HeadroomDB: 6}}, {Bus: 1}}}
		sup, drv := startRole(t, role, busFixture())
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -2 || r.State != StateAtCeiling {
			t.Fatalf("send stops at -8 + 6: %v %s", dbIn(t, r), r.State)
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -2), musician) // the musician's own echo
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -2 {
			t.Fatalf("own echo moved the ceiling: %v", dbIn(t, r))
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -12), desk) // the engineer pulls it down at the desk
		_, _ = sup.HoldWindow(sendLevel(1, 2), musician, true)
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 0), musician)); dbIn(t, r) != -6 || r.Window == nil || r.Window.Ceiling != -6 {
			t.Fatalf("ceiling after the desk: %v %+v", dbIn(t, r), r.Window)
		}
		if r, _ := sup.Write(set(busFader(2), lvl(t, 10), musician)); dbIn(t, r) != 3 || r.State != StateAtCeiling {
			t.Fatalf("My level stops at -3 + 6: %v %s", dbIn(t, r), r.State)
		}
		if r, _ := sup.Write(set(sendLevel(1, 1), lvl(t, 10), musician)); dbIn(t, r) != 10 || r.State != StateFree {
			t.Fatalf("bus 1 has no ceiling: %v %s", dbIn(t, r), r.State)
		}
	})
}

// SR-28: the engineer's devices never meet a ceiling, and a monitor seat's
// ceiling never lowers a value that is already above it.
func TestSend_CeilingNeverLowersAndSkipsEngineers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, monitorOf(2), busFixture())
		if err := sup.SetClientRole("foh", Role{Kind: RoleEngineer}); err != nil {
			t.Fatal(err)
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, 5), musician) // above the ceiling (-8 + 6)
		r, err := sup.Write(set(sendLevel(1, 2), lvl(t, 8), musician))
		if err != nil || dbIn(t, r) != 5 || r.Written || r.State != StateAtCeiling {
			t.Fatalf("kept at +5: %+v %v", r, err)
		}
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 9), engineerB)); dbIn(t, r) != 9 || r.State != StateFree {
			t.Fatalf("engineer: %v %s", dbIn(t, r), r.State)
		}
	})
}

// CR-24: show mode locks raising Gain only; sends and bus faders stay live.
func TestSend_ShowModeLeavesSendsAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		sup.SetShowMode(true)
		for _, w := range []Write{set(sendLevel(1, 2), lvl(t, 4), gesture), set(busFader(2), lvl(t, 6), gesture)} {
			if r, err := sup.Write(w); err != nil || r.State != StateFree || !r.Written {
				t.Fatalf("%s in show mode: %+v %v", w.Param, r, err)
			}
		}
		if n := len(drv.session(0).writes()); n != 2 {
			t.Fatalf("%d writes", n)
		}
	})
}

// AC-SF-05: toggle on a send reads the console's value and writes the inverse once.
func TestSend_ToggleSendOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		if err := sup.Toggle(sendOn(1, 2), gesture); err != nil {
			t.Fatal(err)
		}
		w := drv.session(0).writes()
		if len(w) != 1 || w[0].Param != sendOn(1, 2) || !w[0].Value.Equal(model.Bool(false)) {
			t.Fatalf("writes %+v", w)
		}
		if err := sup.Toggle(sendLevel(1, 2), gesture); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("toggle of a level: %v", err)
		}
	})
}

// Read-only and absent parameters are refused in the core, before the
// driver: a tap, a colour, a link, a matrix, a send the console lacks.
func TestSend_ReadOnlyAndAbsentRefusedInTheCore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		tap, _ := model.TapValue(model.TapPreFader)
		mtx := model.ParamRef{Strip: model.StripRef{Kind: model.StripMatrix, Index: 1}, Field: model.FieldFader}
		for _, w := range []Write{
			set(model.ParamRef{Strip: sendLevel(1, 1).Strip, Field: model.FieldSendTap, Bus: 1}, tap, gesture),
			set(sendLevel(1, 4), lvl(t, 0), gesture), // bus 4 is absent
			set(mtx, lvl(t, 0), gesture),
		} {
			if _, err := sup.Write(w); !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("%s: %v", w.Param, err)
			}
		}
		if err := sup.Hold(mtx, gesture, true); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("hold of a matrix: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}
