// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// x32BusLayout is the X32 layout with the v0.4 additions (x32.md X32-F80..F89).
var x32BusLayout = Layout{Inputs: 32, RequiredInputs: 32, MainRoot: "/main/st", MainMuteGroups: true, MuteGroups: 6, DCAs: 8,
	Buses:  []BusGroup{{First: 1, Count: 16, Root: "/bus/%02d", Kind: model.BusMix, Linkable: true, SendPan: true}},
	SendOn: true, SendPan: true, SendTapLeaf: "type", SendPairScoped: true,
	Matrices: 6, MatrixRoot: "/mtx/%02d", Colours: true, DCAMasters: true}

// xairBusLayout is the X Air layout with the v0.4 additions (xair.md 3.1-3.3).
var xairBusLayout = Layout{Inputs: 16, RequiredInputs: 12, MainRoot: "/lr", MuteGroups: 4, DCAs: 4,
	Buses: []BusGroup{
		{First: 1, Count: 6, Root: "/bus/%d", Kind: model.BusMix, Linkable: true, SendPan: true},
		{First: 7, Count: 4, Root: "/fxsend/%d", Kind: model.BusFX},
	},
	SendPan: true, SendTapLeaf: "tap", Colours: true, DCAMasters: true}

func sendParam(t *testing.T, tbl paramTable, input, bus int, f model.Field) param {
	t.Helper()
	p, ok := tbl.byRef(model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: input}, Field: f, Bus: bus})
	if !ok {
		t.Fatalf("no %s of input %d to bus %d", f, input, bus)
	}
	return p
}

// X32-F83 (S1 p.158) and the "send step" column of xair.md section 4.
func TestSendLevelLaw_ReferencePoints(t *testing.T) {
	p := sendParam(t, newParamTable(x32BusLayout), 1, 1, model.FieldSendLevel)
	for _, tc := range []struct {
		db   float64
		k    int     // step of 160
		back float64 // dB of that step
	}{
		{10, 160, 10}, {5, 140, 5}, {0, 120, 0}, {-5, 100, -5}, {-10, 80, -10}, {-20, 60, -20},
		{-30, 40, -30}, {-40, 30, -40}, {-50, 20, -50}, {-60, 10, -60},
		{-70, 7, -69}, {-80, 3, -81}, {-87, 1, -87}, {math.Inf(-1), 0, math.Inf(-1)},
	} {
		arg, err := toWire(p, mustLevel(t, tc.db))
		if err != nil || arg.Float != float32(float64(tc.k)/160) {
			t.Fatalf("send %v dB -> %v, %v; want k %d of 160", tc.db, arg.Float, err, tc.k)
		}
		v, err := fromWire(p, arg)
		if db, _ := v.DB(); err != nil || !approxDB(db, tc.back, 1e-4) {
			t.Fatalf("send k %d -> %v dB, %v; want %v", tc.k, db, err, tc.back)
		}
	}
	if p.halfStep() != 0.5/160 {
		t.Fatalf("send echo tolerance %v, want half a step of 160", p.halfStep())
	}
	if _, err := toWire(p, model.Bool(true)); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("a bool as send level err = %v", err)
	}
}

func TestSendLevel_AlwaysOnThe161Grid(t *testing.T) {
	p := sendParam(t, newParamTable(xairBusLayout), 3, 2, model.FieldSendLevel)
	for db := -95.0; db <= 11; db += 0.37 {
		arg, err := toWire(p, mustLevel(t, db))
		if err != nil {
			t.Fatal(err)
		}
		if k := float64(arg.Float) * 160; math.Abs(k-math.Round(k)) > 1e-3 {
			t.Fatalf("send %v dB -> %v is not k/160 (XA-F41)", db, arg.Float)
		}
	}
}

// X32-F84 (linf -100..100 step 2), xair.md section 4 (pan, 101 steps).
func TestPanLaw(t *testing.T) {
	p := sendParam(t, newParamTable(x32BusLayout), 1, 1, model.FieldSendPan)
	for _, tc := range []struct {
		pan, back float64
		wire      float32
	}{{-100, -100, 0}, {0, 0, 0.5}, {100, 100, 1}, {-40, -40, 0.3}, {-41, -40, 0.3}, {33, 34, 0.67}} {
		v, _ := model.Pan(tc.pan)
		arg, err := toWire(p, v)
		if err != nil || math.Abs(float64(arg.Float-tc.wire)) > 1e-6 {
			t.Fatalf("pan %v -> %v, %v; want %v", tc.pan, arg.Float, err, tc.wire)
		}
		got, err := fromWire(p, arg)
		if pan, _ := got.Pan(); err != nil || pan != tc.back {
			t.Fatalf("pan wire %v -> %v, %v; want %v", arg.Float, pan, err, tc.back)
		}
	}
	for _, bad := range []float32{-0.01, 1.01, float32(math.NaN())} {
		if _, err := fromWire(p, osc.Float(bad)); !errors.Is(err, errBadWire) {
			t.Fatalf("pan wire %v err = %v", bad, err)
		}
	}
}

// X32-F85, xair.md 3.3: tap 0..5; X32-F89: colour 0..15. Both read-only.
func TestTapAndColourFromWire(t *testing.T) {
	tbl := newParamTable(x32BusLayout)
	tap := sendParam(t, tbl, 1, 1, model.FieldSendTap)
	for wire, want := range []model.Tap{model.TapInput, model.TapPreEQ, model.TapPostEQ, model.TapPreFader, model.TapPostFader, model.TapGroup} {
		v, err := fromWire(tap, osc.Int(int32(wire)))
		if got, _ := v.Tap(); err != nil || got != want {
			t.Fatalf("tap %d -> %v, %v; want %v", wire, got, err, want)
		}
	}
	col, _ := tbl.byRef(pref(model.StripRef{Kind: model.StripBus, Index: 1}, model.FieldColour))
	v, err := fromWire(col, osc.Int(9))
	if c, _ := v.Colour(); err != nil || c.String() != "redInverted" {
		t.Fatalf("colour 9 -> %v, %v", c, err)
	}
	for _, tc := range []struct {
		p    param
		wire int32
	}{{tap, 6}, {tap, -1}, {col, 16}, {col, -1}} {
		if _, err := fromWire(tc.p, osc.Int(tc.wire)); !errors.Is(err, errBadWire) {
			t.Fatalf("%s %d err = %v", tc.p.addr, tc.wire, err)
		}
	}
	for _, p := range []param{tap, col} {
		if !p.readOnly {
			t.Fatalf("%s must be read-only in v0.4", p.addr)
		}
	}
}

func TestTable_X32BusesAndSends(t *testing.T) {
	tbl := newParamTable(x32BusLayout)
	for _, addr := range []string{
		"/bus/01/config/name", "/bus/16/config/color", "/bus/16/mix/fader", "/bus/01/mix/on", // X32-F80
		"/config/buslink/1-2", "/config/buslink/15-16", // X32-F81
		"/ch/01/mix/01/on", "/ch/32/mix/16/level", "/ch/01/mix/02/on", // X32-F82
		"/ch/01/mix/01/pan", "/ch/32/mix/15/type", // X32-F84, X32-F85: odd only
		"/mtx/06/config/name", "/mtx/01/mix/fader", "/mtx/01/mix/on", "/mtx/01/config/color", // X32-F87
		"/dca/8/fader", "/dca/1/config/name", "/dca/1/config/color", // X32-F88
		"/ch/01/config/color", "/main/st/config/color", // X32-F89
	} {
		if _, ok := tbl.byAddress([]byte(addr)); !ok {
			t.Fatalf("x32 table lacks %s", addr)
		}
	}
	for _, addr := range []string{"/ch/01/mix/02/pan", "/ch/01/mix/16/type", "/ch/01/mix/17/level", "/bus/17/mix/fader", "/config/buslink/2-3"} {
		if _, ok := tbl.byAddress([]byte(addr)); ok {
			t.Fatalf("x32 table must not have %s", addr)
		}
	}
	lvl := sendParam(t, tbl, 5, 12, model.FieldSendLevel)
	if lvl.addr != "/ch/05/mix/12/level" || lvl.bus != 12 || lvl.readOnly {
		t.Fatalf("send param %+v", lvl)
	}
	link, _ := tbl.byAddress([]byte("/config/buslink/3-4"))
	if link.ref != pref(model.StripRef{Kind: model.StripBus, Index: 3}, model.FieldLinked) || link.bus != 3 || link.pairBus != 4 || !link.readOnly {
		t.Fatalf("link param %+v", link)
	}
	for _, addr := range []string{"/mtx/02/mix/fader", "/bus/01/config/color"} {
		if p, _ := tbl.byAddress([]byte(addr)); !p.readOnly {
			t.Fatalf("%s must be read-only", addr)
		}
	}
	for _, addr := range []string{"/bus/02/mix/fader", "/bus/02/mix/on", "/dca/3/fader", "/ch/01/mix/02/on"} {
		if p, _ := tbl.byAddress([]byte(addr)); p.readOnly {
			t.Fatalf("%s must be writable", addr)
		}
	}
}

func TestTable_XAirBusesAndSends(t *testing.T) {
	tbl := newParamTable(xairBusLayout)
	for _, addr := range []string{
		"/bus/1/mix/fader", "/bus/6/config/name", "/fxsend/1/mix/on", "/fxsend/4/config/color", // xair 3.1
		"/ch/01/mix/07/level", "/ch/16/mix/06/pan", "/ch/01/mix/02/pan", "/ch/01/mix/02/tap", "/ch/01/mix/07/tap", // xair 3.3
		"/config/buslink/5-6", "/dca/4/fader", "/dca/4/config/color", "/lr/config/color",
	} {
		if _, ok := tbl.byAddress([]byte(addr)); !ok {
			t.Fatalf("xair table lacks %s", addr)
		}
	}
	for _, addr := range []string{"/ch/01/mix/01/on", "/config/buslink/7-8", "/mtx/01/mix/fader", "/bus/01/mix/fader",
		"/ch/01/mix/07/pan", "/ch/16/mix/10/pan"} { // xair.md 3.3 (2026-10-13): no send pan to FX sends 07..10
		if _, ok := tbl.byAddress([]byte(addr)); ok {
			t.Fatalf("xair table must not have %s", addr)
		}
	}
	fx, _ := tbl.byAddress([]byte("/fxsend/2/mix/fader"))
	if fx.ref != pref(model.StripRef{Kind: model.StripBus, Index: 8}, model.FieldFader) || fx.bus != 8 {
		t.Fatalf("FX send 2 is bus 8: %+v", fx)
	}
	opt := sendParam(t, tbl, 14, 3, model.FieldSendLevel)
	if opt.strip != 14 || opt.bus != 3 {
		t.Fatalf("a send of optional input 14 to bus 3 belongs to both: %+v", opt)
	}
	busOrder := slices.IndexFunc(tbl.all, func(p param) bool { return p.bus != 0 })
	inOrder := slices.IndexFunc(tbl.all, func(p param) bool { return p.ref.Field == model.FieldMuteGroups })
	if busOrder < inOrder {
		t.Fatal("v0.4 parameters come after the v0.3 table, so its read order is unchanged")
	}
}

// FuzzSendFromWire: whatever the console sends for a send parameter, the
// model gets a value of the documented encoding or nothing, and a parsed
// level or pan re-encodes onto the console's grid within half a step.
func FuzzSendFromWire(f *testing.F) {
	f.Add(float32(0.75), int32(3))
	f.Add(float32(0.00625), int32(5))
	f.Add(float32(1.5), int32(6))
	f.Add(float32(-0.1), int32(-1))
	tbl := newParamTable(x32BusLayout)
	in1 := func(f model.Field) param {
		p, _ := tbl.byRef(model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: f, Bus: 1})
		return p
	}
	lvl, pan, tap, on := in1(model.FieldSendLevel), in1(model.FieldSendPan), in1(model.FieldSendTap), in1(model.FieldSendOn)
	f.Fuzz(func(t *testing.T, fv float32, iv int32) {
		for _, p := range []param{lvl, pan} {
			v, err := fromWire(p, osc.Float(fv))
			if err != nil {
				continue
			}
			back, err := toWire(p, v)
			if err != nil {
				t.Fatalf("%s: %v does not re-encode: %v", p.addr, v, err)
			}
			steps, wire := p.wireSteps(), math.Min(math.Max(float64(fv), 0), 1)
			k := float64(back.Float) * steps
			if math.Abs(k-math.Round(k)) > 1e-3 || math.Abs(float64(back.Float)-wire) > 0.5/steps+1e-6 {
				t.Fatalf("%s: wire %v -> %v -> %v: off the grid or more than half a step away", p.addr, fv, v, back.Float)
			}
		}
		if v, err := fromWire(tap, osc.Int(iv)); err == nil && (iv < 0 || iv > 5) {
			t.Fatalf("tap %d accepted as %v", iv, v)
		}
		if v, err := fromWire(on, osc.Int(iv)); err == nil && iv != 0 && iv != 1 {
			t.Fatalf("send on %d accepted as %v", iv, v)
		}
	})
}

// x32.md "Send level reference points" (X32-F83, f = k/160): the wire bits
// and dB of every row, both directions.
func TestSendLevelLaw_X32ReferenceTable(t *testing.T) {
	p := sendParam(t, newParamTable(x32BusLayout), 1, 1, model.FieldSendLevel)
	for _, row := range []struct {
		k    int
		bits uint32
		db   float64
	}{
		{0, 0x00000000, math.Inf(-1)}, {1, 0x3bcccccd, -87}, {10, 0x3d800000, -60}, {11, 0x3d8ccccd, -59},
		{40, 0x3e800000, -30}, {41, 0x3e833333, -29.5}, {80, 0x3f000000, -10}, {81, 0x3f01999a, -9.75},
		{119, 0x3f3e6666, -0.25}, {120, 0x3f400000, 0}, {121, 0x3f41999a, 0.25}, {140, 0x3f600000, 5}, {160, 0x3f800000, 10},
	} {
		arg, err := toWire(p, mustLevel(t, row.db))
		if err != nil || math.Float32bits(arg.Float) != row.bits {
			t.Fatalf("k %d: %v dB -> %#08x, %v; want %#08x", row.k, row.db, math.Float32bits(arg.Float), err, row.bits)
		}
		v, err := fromWire(p, osc.Float(math.Float32frombits(row.bits)))
		if db, _ := v.DB(); err != nil || !approxDB(db, row.db, 1e-9) {
			t.Fatalf("k %d: %#08x -> %v dB, %v; want %v", row.k, row.bits, db, err, row.db)
		}
	}
}
