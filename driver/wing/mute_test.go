// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// supervise runs the driver under a real console.Supervisor.
func (r *rig) supervise(t *testing.T) *console.Supervisor {
	t.Helper()
	sup, err := console.New(console.Config{Driver: r.drv, Endpoint: driver.Endpoint{Host: consoleIP}, Options: r.opts(), RereadEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Stop)
	time.Sleep(time.Second)
	synctest.Wait()
	if st := sup.Snapshot().Status; st != console.StatusLive {
		t.Fatalf("status %s", st)
	}
	return sup
}

var ch1Ref = model.StripRef{Kind: model.StripInput, Index: 1}

func groupMute(v int32) *int32 { return &v }

// TestMuteGroup_MemberIsNeverShownLive: review H1. Ch 1 is in mute group 2
// (the console knows, mixcore cannot read it, item 11); engaging the group
// must never leave ch 1 reported as not muted.
func TestMuteGroup_MemberIsNeverShownLive(t *testing.T) {
	cases := []struct {
		name      string
		opts      simwing.Options
		wantKnown bool
	}{
		{"$mute 2 for a group mute (meaning unknown, item 12)", simwing.Options{}, false},
		{"$mute 1 for a group mute", simwing.Options{GroupMuteValue: groupMute(1)}, true},
		{"$mute stays 0 for a group mute (go-reviewer H1)", simwing.Options{GroupMuteValue: groupMute(0)}, false},
		{"no $mute in the schema: derived, partial", simwing.Options{UnknownPaths: []string{"/ch/1/$mute", "/ch/2/$mute"}}, false},
	}
	for _, tc := range cases {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, tc.opts)
			r.sim.SetMuteGroupMember(1, 2, true)
			sup := r.supervise(t)
			if e := sup.Snapshot().State.EffectiveMute(ch1Ref); !e.Known || e.Muted {
				t.Fatalf("%s: before: %+v", tc.name, e)
			}
			mustSet(t, r.sim, "/mgrp/2/mute", 1)
			time.Sleep(200 * time.Millisecond)
			synctest.Wait()
			e := sup.Snapshot().State.EffectiveMute(ch1Ref)
			if e.Known && !e.Muted {
				t.Fatalf("%s: ch 1 reported live while its mute group mutes it: %+v", tc.name, e)
			}
			if e.Known != tc.wantKnown {
				t.Fatalf("%s: %+v", tc.name, e)
			}
			if !sup.Snapshot().Capabilities.MuteGroupMembershipUnknown {
				t.Fatal("capability muteGroupMembership false expected")
			}
		})
	}
}

// TestDCAMgrp_RulesNotMembership: review H1. DCA membership is reported as
// the console has it; /cfg/dcamgrp only changes the rule.
func TestDCAMgrp_RulesNotMembership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{UnknownPaths: []string{"/ch/4/$mute"}})
		mustSet(t, r.sim, "/ch/4/tags", "#D3")
		mustSet(t, r.sim, "/cfg/dcamgrp", 0)
		mustSet(t, r.sim, "/dca/3/mute", 1)
		sup := r.supervise(t)
		ch4 := model.StripRef{Kind: model.StripInput, Index: 4}
		st := sup.Snapshot().State
		if s, _ := st.Strip(ch4); s.DCAs != 1<<2 {
			t.Fatalf("membership must be reported as it is: %b", s.DCAs)
		}
		if e := st.EffectiveMute(ch4); !e.Known || e.Muted {
			t.Fatalf("dcamgrp 0: a DCA mute silences nobody: %+v", e)
		}
		mustSet(t, r.sim, "/cfg/dcamgrp", 1)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if e := sup.Snapshot().State.EffectiveMute(ch4); !e.Known || !e.Muted {
			t.Fatalf("dcamgrp 1: %+v", e)
		}
	})
}
