// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"math"
	"testing"
	"testing/synctest"
	"time"

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

var (
	seatA = driver.Origin{Kind: driver.OriginGesture, ClientID: "mon-a", Seq: 1}
	seatB = driver.Origin{Kind: driver.OriginGesture, ClientID: "mon-b", Seq: 1}
)

// Security re-check A: two seats on one bus. Seat A sends the send down,
// seat B raises to its ceiling, then the engineer cuts it at the desk to
// the very value A sent. A report below the reference is always the
// engineer's lower bound (lead rule): B is now capped at the cut + 6 dB.
func TestCeiling_TwoSeatsMaskEngineerCut(t *testing.T) {
	for _, cut := range []float64{-30, math.Inf(-1)} {
		synctest.Test(t, func(t *testing.T) {
			sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
			if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, cut), seatA)); err != nil {
				t.Fatal(err)
			}
			if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), seatB)); dbIn(t, r) != -2 {
				t.Fatalf("seat B first raise %v, want -8 + 6", dbIn(t, r))
			}
			pushChange(t, drv, sendLevel(1, 2), lvl(t, cut), desk) // the engineer cuts at the desk
			want := finiteOr(cut, ceilingFloorDB) + defaultHeadroomDB
			if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), seatB)); dbIn(t, r) != want {
				t.Fatalf("cut to %v: seat B reaches %v, want %v", cut, dbIn(t, r), want)
			}
		})
	}
}

// Security re-check B: a seat drags -4 then -2; the -2 echo matches, the
// -4 push arrives late as a console report. It is a value the seat sent,
// so it never raises the reference: the ceiling stays at -8 + 6.
func TestCeiling_LateIntermediateEchoNeverRatchets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		_, _ = sup.HoldWindow(sendLevel(1, 2), musician, true)
		for _, v := range []float64{-4, -2} {
			if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, v), musician)); err != nil {
				t.Fatal(err)
			}
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -2), musician) // the matched echo
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -4), desk)     // the late one, unmatched
		_, _ = sup.HoldWindow(sendLevel(1, 2), musician, false)
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -2 {
			t.Fatalf("after a late echo the seat reaches %v, want -2", dbIn(t, r))
		}
	})
}

// An engineer raise at the desk that no seat ever sent is credited.
func TestCeiling_EngineerRaiseNotSeenFromASeatIsCredited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		_, _ = sup.Write(set(sendLevel(1, 2), lvl(t, -5), musician))
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -3), desk)
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != 3 {
			t.Fatalf("after the engineer's -3 the seat reaches %v, want +3", dbIn(t, r))
		}
	})
}

// A revoked seat's values no longer mask the engineer: once seat A is a
// viewer, the engineer raising to A's old value is credited.
func TestCeiling_RevokedSeatNoLongerMasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		_, _ = sup.Write(set(sendLevel(1, 2), lvl(t, -4), seatA))
		if err := sup.SetClientRole(seatA.ClientID, Role{Kind: RoleViewer}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(lateEchoWindowDefault + time.Second)       // A's values can no longer come back as echoes
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -4), desk) // the engineer, at A's old value
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), seatB)); dbIn(t, r) != 2 {
			t.Fatalf("seat B reaches %v, want -4 + 6", dbIn(t, r))
		}
	})
}

// Overflow: more distinct seat values than the bounded set holds refuse
// every raise (the oldest values are forgotten) until a full read at the
// next connect credits as usual; lowering is still credited.
func TestCeiling_OverflowRefusesRaisesUntilAFullRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		for i := range 64 + 1 { // maxSeatValues
			db := -60 + float64(i)*0.5
			_, _ = sup.Write(set(sendLevel(1, 2), lvl(t, min(db, -2)), musician))
			_, _ = sup.Write(set(sendLevel(1, 2), lvl(t, -60-float64(i)*0.25), musician))
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -7), desk) // a raise nobody sent, refused in overflow
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -2 {
			t.Fatalf("overflow credited a raise: seat reaches %v", dbIn(t, r))
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -20), desk) // a cut is always credited
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -14 {
			t.Fatalf("after a cut in overflow the seat reaches %v, want -14", dbIn(t, r))
		}
	})
}

// Final security check, MEDIUM: revoking a seat right after its last raise
// keeps that value for the echo window, so the echo of the raise (now a
// console report) is never taken for the engineer's: seat B stays at -2.
func TestCeiling_RevokeThenEchoNeverRaises(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), seatA)); dbIn(t, r) != -2 {
			t.Fatalf("seat A's raise %v", dbIn(t, r))
		}
		if err := sup.SetClientRole(seatA.ClientID, Role{Kind: RoleViewer}); err != nil {
			t.Fatal(err)
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -2), desk) // A's echo, after the revoke
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), seatB)); dbIn(t, r) != -2 {
			t.Fatalf("seat B reaches %v after A's echo, want -2", dbIn(t, r))
		}
	})
}

// Final security check, LOW: a fast drag of more than 64 values within the
// echo window overflows; a cut right after does not end the overflow while
// a dropped value could still echo, so its late echo is not credited.
func TestCeiling_CutDoesNotEndAFreshOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSeat(t, SafetyConfig{}, monitorOf(2), busFixture())
		first := -59.5
		for i := range 70 { // no time passes: all within one echo window
			_, _ = sup.Write(set(sendLevel(1, 2), lvl(t, first+float64(i)*0.5), musician))
		}
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -60), desk)   // the engineer cuts
		pushChange(t, drv, sendLevel(1, 2), lvl(t, first), desk) // late echo of the first, dropped value
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -54 {
			t.Fatalf("seat reaches %v, want -60 + 6 (the late echo must not be credited)", dbIn(t, r))
		}
		time.Sleep(lateEchoWindowDefault + time.Second)
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -30), desk) // a cut after the window ends the overflow
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -20), desk) // and an engineer raise is credited again
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), musician)); dbIn(t, r) != -14 {
			t.Fatalf("after the window the seat reaches %v, want -20 + 6", dbIn(t, r))
		}
	})
}

// Final security check, LOW: the window comes from the driver's
// capabilities (its echo window), not from a constant that agrees by luck:
// with a 10 s window a revoked seat's value still masks after 3 s.
func TestCeiling_EchoWindowComesFromTheDriver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: busFixture(), caps: gainCaps}
		drv.caps.EchoWindow = 10 * time.Second
		sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Role: monitorOf(2)})
		if err != nil {
			t.Fatal(err)
		}
		if err := sup.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer sup.Stop()
		synctest.Wait()
		_, _ = sup.Write(set(sendLevel(1, 2), lvl(t, -4), seatA))
		_ = sup.SetClientRole(seatA.ClientID, Role{Kind: RoleViewer})
		time.Sleep(3 * time.Second)
		pushChange(t, drv, sendLevel(1, 2), lvl(t, -4), desk)
		if r, _ := sup.Write(set(sendLevel(1, 2), lvl(t, 10), seatB)); dbIn(t, r) != -2 {
			t.Fatalf("seat B reaches %v: a 10 s echo window must keep A's -4 for 3 s", dbIn(t, r))
		}
	})
}
