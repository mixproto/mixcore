// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func send(input, bus int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: input}, Field: f, Bus: bus}
}

func bus(i int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: i}, Field: f}
}

func surface(t *testing.T, sim *simx32.Sim, values map[string]osc.Arg) {
	t.Helper()
	for addr, a := range values {
		if err := sim.SurfaceSet(addr, a); err != nil {
			t.Fatal(err)
		}
	}
}

// Rule 1 and X32-F80..F89: connect reads buses, sends, links, matrices, DCA
// masters and colours, and writes nothing.
func TestConnect_ReadsBusesAndSendsAndWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		surface(t, r.sim, map[string]osc.Arg{
			"/bus/02/config/name": osc.String("Mon 2 Keys"), "/bus/02/config/color": osc.Int(4), "/bus/02/mix/fader": osc.Float(0.6),
			"/bus/02/mix/on": osc.Int(0), "/config/buslink/1-2": osc.Int(1),
			"/ch/04/mix/02/level": osc.Float(130.0 / 160), "/ch/05/mix/02/on": osc.Int(0), // +2.5 dB; Bass send off
			"/ch/01/mix/01/pan": osc.Float(0.3), "/ch/01/mix/01/type": osc.Int(3), // pair 1-2: pan -40, PRE
			"/mtx/03/config/name": osc.String("Delays"), "/mtx/03/mix/fader": osc.Float(0.75),
			"/dca/1/fader": osc.Float(0.5), "/dca/1/config/name": osc.String("Band"), "/ch/01/config/color": osc.Int(9),
		})
		sess := r.connect()
		st, caps := sess.Snapshot(), sess.Capabilities()
		if len(caps.Buses) != 16 || caps.Matrices != 6 || caps.TapLabels[3] != "PRE" || caps.TapLabels[0] != "IN/LC" {
			t.Fatalf("capabilities: buses %d, matrices %d, labels %v", len(caps.Buses), caps.Matrices, caps.TapLabels)
		}
		if sh := st.Shape(); !sh.Colours || !sh.DCAMasters || !sh.BusLinks || sh.Sends != (model.SendShape{Level: true, On: true, Pan: true, Tap: true, PairScoped: true}) {
			t.Fatalf("shape %+v", sh)
		}
		b2, _ := st.Strip(model.StripRef{Kind: model.StripBus, Index: 2})
		if b2.Name != "Mon 2 Keys" || b2.Colour != 4 || !b2.Muted || math.Abs(b2.FaderDB+6) > 0.01 {
			t.Fatalf("bus 2 = %+v", b2)
		}
		want := map[model.ParamRef]model.Value{
			bus(1, model.FieldLinked): model.Bool(true), send(4, 2, model.FieldSendLevel): level(t, 2.5),
			send(5, 2, model.FieldSendOn): model.Bool(false), send(4, 2, model.FieldSendOn): model.Bool(true),
			send(1, 1, model.FieldSendPan): mustPan(t, -40), send(1, 1, model.FieldSendTap): mustTap(t, model.TapPreFader),
		}
		for p, v := range want {
			if got, ok := st.Value(p); !ok || !got.Equal(v) {
				t.Fatalf("%s = %v, %v; want %v", p, got, ok, v)
			}
		}
		if _, ok := st.Value(send(1, 2, model.FieldSendTap)); ok {
			t.Fatal("the tap of an even bus belongs to the pair (X32-F85)")
		}
		if m := st.Matrices(); len(m) != 6 || m[2].Name != "Delays" || math.Abs(m[2].FaderDB) > 0.01 { // 0.75 is stored as step 767 (X32-F41)
			t.Fatalf("matrices %+v", m)
		}
		if d1, _ := st.Strip(model.StripRef{Kind: model.StripDCA, Index: 1}); d1.Name != "Band" || math.Abs(d1.FaderDB+10) > 0.03 {
			t.Fatalf("dca 1 = %+v", d1)
		}
		if in1, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 1}); in1.Colour != 9 {
			t.Fatalf("input 1 colour %v", in1.Colour)
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("connect wrote %d values", n)
		}
	})
}

// X32-F82, X32-F83: a send write goes to exactly one address on the 161-step
// grid and comes back as our echo (gesture origin), never as a desk change.
func TestApply_SendLevelWritesOnlyThatSendAndEchoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		if err := sess.Apply(driver.Command{Param: send(4, 2, model.FieldSendLevel), Value: level(t, 4), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		ev := (<-events).(driver.ParamChanged)
		if ev.Param != send(4, 2, model.FieldSendLevel) || ev.Origin != gesture {
			t.Fatalf("echo = %+v", ev)
		}
		w := r.sim.Writes()
		if len(w) != 1 || w[0].Address != "/ch/04/mix/02/level" || w[0].Arg.Float != 136.0/160 { // +4 dB = k 136
			t.Fatalf("writes %+v", w)
		}
		r.assertWritesAudited()
	})
}

// X32-F82, X32-F84: send on writes 1 = on; pan only on the odd bus of a pair.
func TestApply_SendOnAndPan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		r.run(sess)
		for _, c := range []driver.Command{
			{Param: send(5, 2, model.FieldSendOn), Value: model.Bool(false), Origin: gesture},
			{Param: send(9, 1, model.FieldSendPan), Value: mustPan(t, -100), Origin: gesture},
		} {
			if err := sess.Apply(c); err != nil {
				t.Fatalf("%s: %v", c.Param, err)
			}
		}
		synctest.Wait()
		if a, _ := r.sim.Value("/ch/05/mix/02/on"); a.Int != 0 {
			t.Fatalf("send on = %v, want 0 (off)", a.Int)
		}
		if a, _ := r.sim.Value("/ch/09/mix/01/pan"); a.Float != 0 {
			t.Fatalf("send pan = %v, want 0.0 (hard left)", a.Float)
		}
		err := sess.Apply(driver.Command{Param: send(9, 2, model.FieldSendPan), Value: mustPan(t, 0), Origin: gesture})
		if !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("pan on an even bus = %v, want ErrUnsupported (X32-F84)", err)
		}
		r.assertWritesAudited()
	})
}

// v0.4: taps, colours, links and matrices are read-only; nothing reaches the console.
func TestApply_ReadOnlyBusParameters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		red, _ := model.ColourValue(1)
		for _, c := range []driver.Command{
			{Param: send(1, 1, model.FieldSendTap), Value: mustTap(t, model.TapPostFader)},
			{Param: bus(1, model.FieldColour), Value: red},
			{Param: bus(1, model.FieldLinked), Value: model.Bool(true)},
			{Param: model.ParamRef{Strip: model.StripRef{Kind: model.StripMatrix, Index: 1}, Field: model.FieldFader}, Value: level(t, 0)},
		} {
			c.Origin = gesture
			if err := sess.Apply(c); !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("%s = %v, want ErrUnsupported", c.Param, err)
			}
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}

// X32-F80, X32-F88: bus and DCA faders and a bus mute are written like a channel fader.
func TestApply_BusAndDCAMasters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		r.run(sess)
		for _, c := range []driver.Command{
			{Param: bus(2, model.FieldFader), Value: level(t, -3)},
			{Param: bus(2, model.FieldMute), Value: model.Bool(true)},
			{Param: model.ParamRef{Strip: model.StripRef{Kind: model.StripDCA, Index: 1}, Field: model.FieldFader}, Value: level(t, -5)},
		} {
			c.Origin = gesture
			if err := sess.Apply(c); err != nil {
				t.Fatalf("%s: %v", c.Param, err)
			}
		}
		synctest.Wait()
		for addr, want := range map[string]osc.Arg{"/bus/02/mix/on": osc.Int(0), "/dca/1/fader": osc.Float(float32(math.Round(0.625*1023) / 1023))} {
			if a, _ := r.sim.Value(addr); !a.Equal(want) {
				t.Fatalf("%s = %v, want %v", addr, a, want)
			}
		}
		if a, _ := r.sim.Value("/bus/02/mix/fader"); math.Abs(float64(a.Float)-0.675) > 0.5/1023 {
			t.Fatalf("bus 2 fader = %v, want -3 dB", a.Float)
		}
		r.assertWritesAudited()
	})
}

// X32-F90: a send moved at the desk is a console change; no write follows.
func TestSurfaceSend_ReportedWithConsoleOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		_ = r.sim.SurfaceSet("/ch/03/mix/16/level", osc.Float(0.5))
		ev := (<-events).(driver.ParamChanged)
		if ev.Param != send(3, 16, model.FieldSendLevel) || ev.Origin.Kind != driver.OriginConsole || !ev.Value.Equal(level(t, -10)) {
			t.Fatalf("event = %+v", ev)
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("a console change must not cause a write")
		}
	})
}

// Never faked: a console that does not answer colours and matrices connects
// without them; buses, sends and DCA masters stay.
func TestConnect_UnansweredColoursAndMatricesAreAbsent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{UnknownAddresses: []string{"/ch/07/config/color", "/mtx/04/mix/on"}})
		sess := r.connect()
		st, caps := sess.Snapshot(), sess.Capabilities()
		if sh := st.Shape(); sh.Colours || !sh.DCAMasters || !sh.Sends.Level || caps.Matrices != 0 || len(st.Matrices()) != 0 || len(caps.Buses) != 16 {
			t.Fatalf("shape %+v, matrices %d/%d, buses %d", sh, caps.Matrices, len(st.Matrices()), len(caps.Buses))
		}
		if _, ok := st.Value(bus(1, model.FieldColour)); ok {
			t.Fatal("colours are absent for every strip once one does not answer")
		}
		red, _ := model.ColourValue(1)
		if err := sess.Apply(driver.Command{Param: bus(1, model.FieldColour), Value: red, Origin: gesture}); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("absent colour Apply = %v", err)
		}
	})
}

func mustPan(t *testing.T, p float64) model.Value {
	t.Helper()
	v, err := model.Pan(p)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustTap(t *testing.T, tap model.Tap) model.Value {
	t.Helper()
	v, err := model.TapValue(tap)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
