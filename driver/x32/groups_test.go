// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func grp(kind model.StripKind, i int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: kind, Index: i}, Field: model.FieldMute}
}

// X32-F35..F38: groups, DCA mutes and membership are read at connect, with
// the inverted polarity of /config/mute handled here.
func TestConnect_ReadsGroupsAndMembership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		for addr, a := range map[string]osc.Arg{
			"/ch/03/grp/mute": osc.Int(1 << 5), "/config/mute/6": osc.Int(1),
			"/ch/03/grp/dca": osc.Int(1), "/dca/1/on": osc.Int(0), "/main/st/grp/mute": osc.Int(1),
		} {
			if err := r.sim.SurfaceSet(addr, a); err != nil {
				t.Fatal(err)
			}
		}
		sess := r.connect()
		st := sess.Snapshot()
		if caps := sess.Capabilities(); caps.MuteGroups != 6 || caps.DCAs != 8 {
			t.Fatalf("capabilities = %+v", caps)
		}
		if mg := st.MuteGroups(); len(mg) != 6 || !mg[5].Muted || mg[0].Muted {
			t.Fatalf("mute groups = %+v", mg)
		}
		if d := st.DCAs(); len(d) != 8 || !d[0].Muted || d[1].Muted {
			t.Fatalf("DCAs = %+v", d)
		}
		ch3 := model.StripRef{Kind: model.StripInput, Index: 3}
		e := st.EffectiveMute(ch3)
		if !e.Muted || e.Own || e.MuteGroups != 1<<5 || e.DCAs != 1 {
			t.Fatalf("effective mute of ch 3 = %+v", e)
		}
		if m, _ := st.Main(); m.MuteGroups != 1 {
			t.Fatalf("main membership = %b", m.MuteGroups)
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("connect wrote")
		}
	})
}

// A mute group write uses the group's polarity (1 = engaged); a DCA mute the
// channel polarity (0 = muted); membership is read-only.
func TestApply_GroupMutesUseTheirPolarity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		for _, p := range []model.ParamRef{grp(model.StripMuteGroup, 2), grp(model.StripDCA, 1)} {
			if err := sess.Apply(driver.Command{Param: p, Value: model.Bool(true), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		if v, _ := r.sim.Value("/config/mute/2"); v.Int != 1 {
			t.Fatalf("/config/mute/2 = %d, want 1 (engaged)", v.Int)
		}
		if v, _ := r.sim.Value("/dca/1/on"); v.Int != 0 {
			t.Fatalf("/dca/1/on = %d, want 0 (muted)", v.Int)
		}
		member := model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldMuteGroups}
		if err := sess.Apply(driver.Command{Param: member, Value: model.Mask(1), Origin: gesture}); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("membership write err = %v, want ErrUnsupported", err)
		}
		_ = r.sim.SurfaceSet("/config/mute/4", osc.Int(1)) // engaged at the desk
		for {
			ev := (<-events).(driver.ParamChanged)
			if ev.Param == grp(model.StripMuteGroup, 4) {
				if !ev.Value.Equal(model.Bool(true)) || ev.Origin.Kind != driver.OriginConsole {
					t.Fatalf("push = %+v", ev)
				}
				break
			}
		}
		r.assertWritesAudited()
	})
}

// A console that does not know the group addresses (X32-F06: no reply) still
// connects; the features are absent, never faked.
func TestConnect_UnknownGroupAddressesDropTheFeature(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{UnknownAddresses: []string{"/config/mute/3", "/ch/07/grp/dca"}})
		sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, r.opts())
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		defer sess.Close()
		caps := sess.Capabilities()
		if caps.MuteGroups != 0 || caps.DCAs != 0 || len(sess.Snapshot().MuteGroups()) != 0 {
			t.Fatalf("capabilities %+v with groups %v; want both features absent", caps, sess.Snapshot().MuteGroups())
		}
		if err := sess.Apply(driver.Command{Param: grp(model.StripMuteGroup, 1), Value: model.Bool(true), Origin: gesture}); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("write to an absent feature err = %v", err)
		}
	})
}
