// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

func send(input, bus int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: input}, Field: f, Bus: bus}
}

func busRef(i int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: i}, Field: f}
}

// xair.md 3.1-3.3: buses 1..6 and FX sends as 7..10; pan and tap per send;
// no send on/off. Connect writes nothing.
func TestConnect_ReadsBusesAndPerSendPanAndTap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		for addr, a := range map[string]osc.Arg{
			"/bus/2/config/name": osc.String("Keys IEM"), "/fxsend/1/config/name": osc.String("Reverb"),
			"/ch/09/mix/02/level": osc.Float(0.75), "/ch/09/mix/02/pan": osc.Float(0.7), "/ch/09/mix/02/tap": osc.Int(3),
			"/config/buslink/1-2": osc.Int(1), "/dca/2/fader": osc.Float(0.5), "/dca/2/config/color": osc.Int(6),
		} {
			if err := r.sim.SurfaceSet(addr, a); err != nil {
				t.Fatal(err)
			}
		}
		sess := r.connect()
		st := sess.Snapshot()
		if sh := st.Shape(); sh.Sends != (model.SendShape{Level: true, Pan: true, Tap: true}) || !sh.BusLinks || !sh.Colours || !sh.DCAMasters {
			t.Fatalf("shape %+v", sh)
		}
		if b7, _ := st.Strip(model.StripRef{Kind: model.StripBus, Index: 7}); b7.Name != "Reverb" {
			t.Fatalf("FX send 1 is bus 7: %+v", b7)
		}
		pan, _ := model.Pan(40)
		pre, _ := model.TapValue(model.TapPreFader)
		for p, want := range map[model.ParamRef]model.Value{
			send(9, 2, model.FieldSendLevel): level(t, 0), send(9, 2, model.FieldSendPan): pan, send(9, 2, model.FieldSendTap): pre,
			busRef(1, model.FieldLinked): model.Bool(true),
		} {
			if got, ok := st.Value(p); !ok || !got.Equal(want) {
				t.Fatalf("%s = %v, %v; want %v", p, got, ok, want)
			}
		}
		if _, ok := st.Value(send(9, 2, model.FieldSendOn)); ok {
			t.Fatal("X Air has no send on/off (section 3.3): never faked")
		}
		if _, ok := st.Value(busRef(7, model.FieldLinked)); ok {
			t.Fatal("FX sends are not linkable")
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("connect wrote %d values", n)
		}
	})
}

// Section 3.3: a send write leaves for /ch/NN/mix/MM/level (FX send 1 =
// slot 07), its echo carries our origin; a send on is unsupported.
func TestApply_SendToAnFXBusAndNoSendOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		if err := sess.Apply(driver.Command{Param: send(10, 7, model.FieldSendLevel), Value: level(t, -10), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if ev := (<-events).(driver.ParamChanged); ev.Param != send(10, 7, model.FieldSendLevel) || ev.Origin != gesture {
			t.Fatalf("echo = %+v", ev)
		}
		if w := r.sim.Writes(); len(w) != 1 || w[0].Address != "/ch/10/mix/07/level" || w[0].Arg.Float != 0.5 {
			t.Fatalf("writes %+v", w)
		}
		err := sess.Apply(driver.Command{Param: send(10, 2, model.FieldSendOn), Value: model.Bool(false), Origin: gesture})
		if !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("send on = %v, want ErrUnsupported", err)
		}
		r.assertWritesAudited()
	})
}

// XA-F72, test item 12: a mixer with only two buses connects without buses
// 3..6 and every send to them; the FX sends stay. A reconnect over the
// link knows the buses and waits for no dead read.
func TestLink_MixerWithTwoBusesConnectsWithoutTheOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{Buses: 2})
		l := r.openLink(t, r.opts())
		sess, first := timedConnect(t, l)
		caps := sess.Capabilities()
		var got []int
		for _, b := range caps.Buses {
			got = append(got, b.Index)
		}
		if len(got) != 6 || got[1] != 2 || got[2] != 7 || first > reconnectTarget {
			t.Fatalf("buses %v in %s", got, first)
		}
		if _, ok := sess.Snapshot().Value(send(1, 3, model.FieldSendLevel)); ok {
			t.Fatal("a send to an absent bus must be absent")
		}
		err := sess.Apply(driver.Command{Param: send(1, 3, model.FieldSendLevel), Value: level(t, 0), Origin: gesture})
		if !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("send to an absent bus = %v", err)
		}
		_ = sess.Close()
		sess, again := timedConnect(t, l)
		defer sess.Close()
		if n := len(sess.Capabilities().Buses); n != 6 || again > 100*time.Millisecond {
			t.Fatalf("reconnect: %d buses in %s", n, again)
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}
