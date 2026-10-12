// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/internal/osc"
)

const toggleMG3 = `{"version":0,"type":"toggle","param":{"strip":{"kind":"muteGroup","index":3},"field":"mute"},"origin":{"kind":"gesture","clientId":"ui","seq":5}}`

// CR-01, CR-02: a mute group toggle is one gesture (read state, write the
// inverse); the echo brings the group state and the member's effective mute.
func TestToggle_MuteGroupAndEffectiveMute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		_ = e.sim.SurfaceSet("/ch/02/grp/mute", osc.Int(1<<2)) // ch 2 is in mute group 3
		e.startLive(t)
		snap := e.rec.last()
		if len(snap.MuteGroups) != 6 || len(snap.DCAs) != 8 || snap.Strips[1].EffectiveMute == nil || snap.Strips[1].EffectiveMute.Muted {
			t.Fatalf("snapshot groups %v, DCAs %v, ch2 %+v", snap.MuteGroups, snap.DCAs, snap.Strips[1])
		}
		if err := e.eng.Apply(toggleMG3); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if v, _ := e.sim.Value("/config/mute/3"); v.Int != 1 {
			t.Fatalf("/config/mute/3 = %d after the toggle, want 1 (engaged)", v.Int)
		}
		d := e.rec.last()
		var sawGroup, sawEffective bool
		for _, c := range *d.Changes {
			switch {
			case c.Param.Strip.Kind == "muteGroup" && c.Param.Strip.Index == 3 && c.Value == true && c.Origin.ClientID == "ui":
				sawGroup = true
			case c.Param.Field == "effectiveMute" && c.Param.Strip.Index == 2 && c.Origin.ClientID == "ui":
				eff := c.Value.(map[string]any)
				sawEffective = eff["muted"] == true && eff["own"] == false
			}
		}
		if !sawGroup || !sawEffective {
			t.Fatalf("diff after the toggle = %+v", *d.Changes)
		}
		if err := e.eng.Apply(toggleMG3); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if v, _ := e.sim.Value("/config/mute/3"); v.Int != 0 {
			t.Fatalf("second toggle: /config/mute/3 = %d, want 0", v.Int)
		}
		if n := len(e.sim.Writes()); n != 2 {
			t.Fatalf("%d writes, want one per toggle", n)
		}
	})
}

func TestApply_GroupCommandErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		for cmdJSON, want := range map[string]string{
			`{"version":0,"type":"toggle","param":{"strip":{"kind":"input","index":1},"field":"fader"},"origin":{"kind":"gesture"}}`:                   CodeUnsupported,
			`{"version":0,"type":"toggle","param":{"strip":{"kind":"muteGroup","index":3},"field":"mute"},"value":true,"origin":{"kind":"gesture"}}`:   CodeInvalidCommand,
			`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"muteGroups"},"value":[1],"origin":{"kind":"gesture"}}`:     CodeUnsupported,
			`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"muteGroups"},"value":[0],"origin":{"kind":"gesture"}}`:     CodeInvalidCommand,
			`{"version":0,"type":"set","param":{"strip":{"kind":"dca","index":0},"field":"mute"},"value":true,"origin":{"kind":"gesture"}}`:            CodeInvalidCommand,
			`{"version":0,"type":"toggle","param":{"strip":{"kind":"dca","index":9},"field":"mute"},"origin":{"kind":"gesture"}}`:                      CodeUnsupported,
			`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"effectiveMute"},"value":true,"origin":{"kind":"gesture"}}`: CodeInvalidCommand,
		} {
			if err := e.eng.Apply(cmdJSON); CodeOf(err) != want {
				t.Errorf("%s: %v, want %s", cmdJSON, err, want)
			}
		}
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("%d writes from refused commands", n)
		}
	})
}
