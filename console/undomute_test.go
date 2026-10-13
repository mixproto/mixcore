// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Coordinator rule for the tri-state effective mute (wing): on a strip
// whose effective mute is unknown (muted null: an engaged mute group of
// unknown membership), an undo never unmutes without asking. Undoing an
// own mute, or the engaging of a mute group (which may hold any strip),
// asks; undoing an unmute (it mutes) never needs to.
func TestUndo_UnknownEffectiveMuteNeverUnmutesWithoutAsking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := safetyState(-6).WithGroups(1, 0).WithMuteRules(model.MuteRules{MuteGroupMembershipUnknown: true})
		mg1 := model.ParamRef{Strip: model.StripRef{Kind: model.StripMuteGroup, Index: 1}, Field: model.FieldMute}
		ch1Mute := model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldMute}
		sup, drv := startSafe(t, SafetyConfig{}, st)
		toggleEchoed := func(p model.ParamRef, to bool) {
			t.Helper()
			if err := sup.Toggle(p, gesture); err != nil {
				t.Fatal(err)
			}
			drv.session(0).push <- driver.ParamChanged{Param: p, Value: model.Bool(to), Origin: gesture}
			synctest.Wait()
		}
		toggleEchoed(mg1, true) // engage the group: ch 1's effective mute becomes unknown
		if em := sup.Snapshot().State.EffectiveMute(ch1Mute.Strip); em.Known {
			t.Fatalf("fixture: ch 1 effective mute %+v, want unknown", em)
		}
		toggleEchoed(ch1Mute, true)  // mute the strip itself
		toggleEchoed(ch1Mute, false) // and unmute it again
		n := len(drv.session(0).writes())
		if r, err := sup.Undo(gesture, false); err != nil || len(drv.session(0).writes()) != n+1 || !r.Written {
			t.Fatalf("undoing an unmute mutes: %+v %v", r, err) // it mutes: no question
		}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Mute, Value: model.Bool(true), Origin: gesture}
		synctest.Wait()
		for _, p := range []model.ParamRef{ch1Mute, mg1} { // the own mute, then the group's engaging
			e := newestUndo(t, sup.Drain(), gesture.ClientID)
			if e.Param != p || !slices.Contains(e.Asks, AskUnmute) {
				t.Fatalf("entry %+v, want %s asking unmute", e, p)
			}
			if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNeedsConfirm) || len(drv.session(0).writes()) != n+1 {
				t.Fatalf("an undo that unmutes went through without asking: %v", err)
			}
			if _, err := sup.Undo(gesture, true); err != nil {
				t.Fatal(err)
			}
			n++
			drv.session(0).push <- driver.ParamChanged{Param: p, Value: model.Bool(false), Origin: gesture}
			synctest.Wait()
		}
	})
}
