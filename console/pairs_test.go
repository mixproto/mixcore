// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/model"
)

// pairFixture is busFixture with bus links reported, the pair 1-2 linked
// or not, x32 pair-scoped send pan (X32-F84) and an FX bus 7.
func pairFixture(linked bool) model.State {
	st, err := safetyState(-6).WithBuses([]model.BusInfo{
		{Index: 1, Kind: model.BusMix, Linkable: true, SendPan: true}, {Index: 2, Kind: model.BusMix, Linkable: true, SendPan: true},
		{Index: 7, Kind: model.BusFX},
	})
	if err != nil {
		panic(err)
	}
	st = st.WithShape(model.Shape{BusLinks: true, Sends: model.SendShape{Level: true, On: true, Pan: true, PairScoped: true}})
	if st, err = st.With(busLinked(1), model.Bool(linked)); err != nil {
		panic(err)
	}
	return st
}

func busLinked(b int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: b}, Field: model.FieldLinked}
}

func sendPan(input, bus int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: input}, Field: model.FieldSendPan, Bus: bus}
}

// Sec M2: a grant for one bus of a linked pair must not change its
// partner (the console may mirror the write, X32-F81 item 25): ERR_ROLE
// unless the seat holds both buses.
func TestRole_LinkedPairNeedsBothGrants(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startRole(t, monitorOf(2), pairFixture(true))
		for _, p := range []model.ParamRef{sendLevel(1, 2), busFader(2), busMute(2)} {
			v := lvl(t, -20)
			if p.Field == model.FieldMute {
				v = model.Bool(true)
			}
			if _, err := sup.Write(set(p, v, musician)); !errors.Is(err, ErrRole) {
				t.Fatalf("%s with a grant for bus 2 only of the linked pair: %v", p, err)
			}
		}
		if err := sup.SetClientRole("mon-2", monitorOf(1, 2)); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, -20), musician)); err != nil {
			t.Fatalf("both buses granted: %v", err)
		}
	})
}

// Sec M2, X32-F84: the send pan of a pair lives on the odd send and acts
// for both buses, linked or not.
func TestRole_PairSharedSendPanNeedsBothGrants(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startRole(t, monitorOf(1), pairFixture(false))
		pan, _ := model.Pan(-40)
		if _, err := sup.Write(set(sendPan(1, 1), pan, musician)); !errors.Is(err, ErrRole) {
			t.Fatalf("pair pan with bus 1 only: %v", err)
		}
		if _, err := sup.Write(set(sendLevel(1, 1), lvl(t, -20), musician)); err != nil {
			t.Fatalf("an unlinked bus's own send level: %v", err)
		}
		if err := sup.SetClientRole("mon-2", monitorOf(1, 2)); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(sendPan(1, 1), pan, musician)); err != nil {
			t.Fatalf("pair pan with both buses: %v", err)
		}
	})
}

// Sec M2: linking a pair at the console re-checks the roles at once; a
// hold the seat may no longer move ends (holdCancelled, reason role).
func TestRole_LinkChangeCancelsHoldsOutsideTheGrant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, monitorOf(2), pairFixture(false))
		if err := sup.Hold(sendLevel(1, 2), musician, true); err != nil {
			t.Fatal(err)
		}
		_ = sup.Drain()
		pushChange(t, drv, busLinked(1), model.Bool(true), desk)
		u := sup.Drain()
		if len(u.Events) != 1 || u.Events[0].Kind != HoldCancelled || u.Events[0].Reason != ReasonRole {
			t.Fatalf("events %+v", u.Events)
		}
		if _, err := sup.Write(set(sendLevel(1, 2), lvl(t, -20), musician)); !errors.Is(err, ErrRole) {
			t.Fatalf("after the link: %v", err)
		}
	})
}

// Sec L3: an FX send master (bus kind fx) is not a monitor mix; a seat
// writes into it only with a grant that says so.
func TestRole_FXBusNeedsAnFXGrant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startRole(t, monitorOf(7), pairFixture(false))
		if _, err := sup.Write(set(sendLevel(1, 7), lvl(t, -20), musician)); !errors.Is(err, ErrRole) {
			t.Fatalf("FX bus without an FX grant: %v", err)
		}
		if err := sup.SetClientRole("mon-2", Role{Kind: RoleMonitor, Buses: []BusGrant{{Bus: 7, FX: true}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(sendLevel(1, 7), lvl(t, -20), musician)); err != nil {
			t.Fatalf("FX grant: %v", err)
		}
	})
}
