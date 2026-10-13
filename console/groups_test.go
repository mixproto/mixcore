// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

var (
	mg2     = model.ParamRef{Strip: model.StripRef{Kind: model.StripMuteGroup, Index: 2}, Field: model.FieldMute}
	ch1     = model.StripRef{Kind: model.StripInput, Index: 1}
	ch1Mute = model.ParamRef{Strip: ch1, Field: model.FieldMute}
)

// stateWithGroups: two inputs (ch 1 in mute group 2), main, 6 groups, 8 DCAs.
func stateWithGroups() model.State {
	inputs := []model.Strip{
		{Ref: ch1, MuteGroups: 0b10},
		{Ref: model.StripRef{Kind: model.StripInput, Index: 2}},
	}
	main := model.Strip{Ref: model.MainRef, FaderDB: math.Inf(-1)}
	st, err := model.NewState(inputs, &main)
	if err != nil {
		panic(err)
	}
	return st.WithGroups(6, 8)
}

func console(origin driver.Origin, p model.ParamRef, v model.Value) driver.ParamChanged {
	return driver.ParamChanged{Param: p, Value: v, Origin: origin}
}

// CR-01: when a group engages, every member's effective mute changes in the
// same update, with the origin of the change that caused it.
func TestEffectiveMute_FollowsGroupsInDiffs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWithGroups()}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		desk := driver.Origin{Kind: driver.OriginConsole}
		drv.session(0).push <- console(desk, mg2, model.Bool(true))
		synctest.Wait()
		u := sup.Drain()
		if len(u.Effective) != 1 || u.Effective[0].Strip != ch1 || !u.Effective[0].Mute.Muted || u.Effective[0].Mute.MuteGroups != 0b10 ||
			u.Effective[0].Origin != desk {
			t.Fatalf("effective changes = %+v", u.Effective)
		}
		drv.session(0).push <- console(desk, ch1Mute, model.Bool(true)) // own mute while the group holds it: still muted, sources change
		synctest.Wait()
		if u := sup.Drain(); len(u.Effective) != 1 || !u.Effective[0].Mute.Own {
			t.Fatalf("own mute added: %+v", u.Effective)
		}
		drv.session(0).push <- console(desk, model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 2}, Field: model.FieldFader}, lvl(t, -3))
		synctest.Wait()
		if u := sup.Drain(); len(u.Effective) != 0 {
			t.Fatalf("a fader change must not list effective mutes: %+v", u.Effective)
		}
	})
}

// A toggle reads the state in the core and writes the inverse, as a gesture.
func TestToggle_WritesTheInverseOfTheConsoleValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWithGroups()}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Toggle(mg2, gesture); err != nil {
			t.Fatal(err)
		}
		w := drv.session(0).writes()
		if len(w) != 1 || w[0].Param != mg2 || !w[0].Value.Equal(model.Bool(true)) || w[0].Origin != gesture {
			t.Fatalf("writes = %+v, want group 2 engaged", w)
		}
		if err := sup.Toggle(ch1Fader, gesture); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("toggle of a fader err = %v", err)
		}
		if err := sup.Toggle(mg2, driver.Origin{Kind: driver.OriginSystem}); !errors.Is(err, driver.ErrNotUserOrigin) {
			t.Fatalf("system toggle err = %v", err)
		}
		missing := model.ParamRef{Strip: model.StripRef{Kind: model.StripDCA, Index: 9}, Field: model.FieldMute}
		if err := sup.Toggle(missing, gesture); !errors.Is(err, model.ErrUnknownParam) {
			t.Fatalf("toggle of an unknown group err = %v", err)
		}
	})
}

// SR-15: a scene recall ends every held fader at once, even one whose own
// update has not arrived; the recall is reported once.
func TestSceneRecall_EndsEveryHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWithGroups()}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		sup.Drain()
		drv.session(0).push <- driver.SceneRecalled{Confidence: driver.ConfidenceLow, Signals: []string{driver.SignalBurst}, HighlightedScene: driver.NoScene}
		synctest.Wait()
		u := sup.Drain()
		if len(u.Scenes) != 1 || len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange || u.Events[0].Origin.Kind != driver.OriginConsole {
			t.Fatalf("scenes %+v events %+v", u.Scenes, u.Events)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("Apply after the recall = %v, want ErrGestureEnded", err)
		}
		if u := sup.Drain(); len(u.Scenes) != 0 {
			t.Fatal("a scene is reported once")
		}
	})
}

// Review H1: the console's own mute report (wing $mute) and a change of
// the mute rules (wing /cfg/dcamgrp) both update effective mutes, with
// unknown reported as unknown, never as live.
func TestEffectiveMute_ConsoleReportAndRules(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		snap := stateWithGroups().WithMuteRules(model.MuteRules{MuteGroupMembershipUnknown: true})
		drv := &fakeDriver{snapshot: snap}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		desk := driver.Origin{Kind: driver.OriginConsole}
		drv.session(0).push <- console(desk, mg2, model.Bool(true))
		synctest.Wait()
		if u := sup.Drain(); len(u.Effective) != 3 || u.Effective[0].Mute.Known || u.Effective[0].Mute.Basis != model.BasisPartial {
			t.Fatalf("an engaged group of unknown membership: %+v", u.Effective)
		}
		on, _ := model.Index(model.ConsoleMuteOn)
		drv.session(0).push <- console(desk, model.ParamRef{Strip: ch1, Field: model.FieldConsoleMute}, on)
		synctest.Wait()
		u := sup.Drain()
		if len(u.Effective) != 1 || !u.Effective[0].Mute.Known || !u.Effective[0].Mute.Muted || u.Effective[0].Mute.Basis != model.BasisConsole {
			t.Fatalf("the console's report: %+v", u.Effective)
		}
		drv.session(0).push <- driver.MuteRulesChanged{Rules: model.MuteRules{}}
		synctest.Wait()
		u = sup.Drain()
		for _, e := range u.Effective {
			if !e.Mute.Known {
				t.Fatalf("rules changed, membership known: %+v", u.Effective)
			}
		}
		if len(u.Effective) == 0 {
			t.Fatal("rules changed: no effective change listed")
		}
		if r := sup.Snapshot().State.MuteRules(); r.MuteGroupMembershipUnknown {
			t.Fatal("the state must carry the new rules")
		}
	})
}
