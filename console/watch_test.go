// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Redesign CC-11 (core-api v0.5 rule 15, AC-RD-X02): a client names the
// bus it shows; changes in that scope by anyone else are reported at once
// for the first, then at most once per 2 s while they continue; its own
// echoes and changes outside the scope never count.
func TestWatch_ExternalChangesInScopeCoalescePerTwoSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		if err := sup.Watch(gesture, 2); err != nil {
			t.Fatal(err)
		}
		push := func(p model.ParamRef, v model.Value, o driver.Origin) {
			drv.session(0).push <- driver.ParamChanged{Param: p, Value: v, Origin: o}
			synctest.Wait()
		}
		_ = sup.Drain()
		push(sendLevel(1, 2), lvl(t, -6), gesture)   // own echo: not counted
		push(sendLevel(1, 1), lvl(t, -4), desk)      // bus 1: out of scope
		push(sendLevel(1, 2), lvl(t, -5), engineerB) // another client: counted, reported at once
		if u := sup.Drain(); !slices.Equal(u.Scopes, []ScopeEvent{{ClientID: gesture.ClientID, Bus: 2, Count: 1}}) {
			t.Fatalf("first change: %+v", u.Scopes)
		}
		push(busFader(2), lvl(t, -9), desk)
		push(busMute(2), model.Bool(true), desk)
		if u := sup.Drain(); len(u.Scopes) != 0 {
			t.Fatalf("within 2 s: %+v", u.Scopes)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if u := sup.Drain(); !slices.Equal(u.Scopes, []ScopeEvent{{ClientID: gesture.ClientID, Bus: 2, Count: 2}}) {
			t.Fatalf("after 2 s: %+v", u.Scopes)
		}
		if err := sup.Watch(gesture, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Second)
		push(busFader(2), lvl(t, -12), desk)
		if u := sup.Drain(); len(u.Scopes) != 0 || len(drv.session(0).writes()) != 0 {
			t.Fatalf("after clearing: %+v, %d writes", u.Scopes, len(drv.session(0).writes()))
		}
	})
}

// The partner of a linked pair is in scope: the pair is one stereo mix.
func TestWatch_LinkedPartnerIsInScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := busFixture().WithShape(model.Shape{BusLinks: true, Sends: model.SendShape{Level: true, On: true, Pan: true, PairScoped: true}})
		sup, drv := startRole(t, Role{}, st)
		linked := model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: 1}, Field: model.FieldLinked}
		drv.session(0).push <- driver.ParamChanged{Param: linked, Value: model.Bool(true), Origin: desk}
		synctest.Wait()
		_ = sup.Watch(gesture, 1)
		_ = sup.Drain()
		drv.session(0).push <- driver.ParamChanged{Param: sendLevel(1, 2), Value: lvl(t, -20), Origin: desk}
		synctest.Wait()
		if u := sup.Drain(); len(u.Scopes) != 1 || u.Scopes[0].Bus != 1 {
			t.Fatalf("partner change: %+v", u.Scopes)
		}
	})
}

// At most maxWatches clients watch; a new one drops the oldest watch, whose
// client then gets no event.
func TestWatch_BoundedByDroppingTheOldest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startRole(t, Role{}, busFixture())
		for i := range maxWatches + 1 {
			o := driver.Origin{Kind: driver.OriginGesture, ClientID: fmt.Sprintf("dev-%02d", i)}
			if err := sup.Watch(o, 2); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		_ = sup.Drain()
		drv.session(0).push <- driver.ParamChanged{Param: busFader(2), Value: lvl(t, -9), Origin: desk}
		synctest.Wait()
		u := sup.Drain()
		if len(u.Scopes) != maxWatches || slices.ContainsFunc(u.Scopes, func(e ScopeEvent) bool { return e.ClientID == "dev-00" }) {
			t.Fatalf("%d events, dev-00 dropped: %+v", len(u.Scopes), u.Scopes)
		}
	})
}
