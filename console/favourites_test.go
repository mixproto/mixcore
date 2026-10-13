// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// Redesign CC-7 (core-api v0.5 rule 17, AC-RD-P08): one ordered favourites
// layer per console, at most 32 strips, each once; it never writes.
func TestFavourites_SetValidatesAndNeverWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := func(i int) model.StripRef { return model.StripRef{Kind: model.StripInput, Index: i} }
		bus2 := model.StripRef{Kind: model.StripBus, Index: 2}
		cfgList := []model.StripRef{in(3), model.MainRef}
		drv := &fakeDriver{snapshot: safetyState(-6), caps: gainCaps}
		sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}}, Favourites: cfgList})
		if err != nil {
			t.Fatal(err)
		}
		_ = sup.Start(context.Background())
		t.Cleanup(sup.Stop)
		synctest.Wait()
		if u := sup.Drain(); !slices.Equal(u.Favourites, cfgList) {
			t.Fatalf("restored %v", u.Favourites)
		}
		want := []model.StripRef{bus2, in(1), model.MainRef}
		if err := sup.SetFavourites(gesture, want); err != nil {
			t.Fatal(err)
		}
		if u := sup.Drain(); !slices.Equal(u.Favourites, want) {
			t.Fatalf("favourites %v", u.Favourites)
		}
		long := make([]model.StripRef, 33)
		for i := range long {
			long[i] = in(i + 1)
		}
		for _, bad := range [][]model.StripRef{long, {in(1), in(1)}, {{Kind: model.StripHeadamp, Index: 3}}, {{Kind: model.StripMuteGroup, Index: 1}}, {in(0)}} {
			if err := sup.SetFavourites(gesture, bad); err == nil {
				t.Fatalf("%v accepted", bad)
			}
		}
		if u := sup.Drain(); !slices.Equal(u.Favourites, want) || len(drv.session(0).writes()) != 0 {
			t.Fatalf("after refusals: %v, %d writes", u.Favourites, len(drv.session(0).writes()))
		}
	})
}
