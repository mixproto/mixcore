// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"errors"
	"math"
	"testing"
)

func busRef(i int) StripRef { return StripRef{Kind: StripBus, Index: i} }

func sendRef(input, bus int, f Field) ParamRef {
	return ParamRef{Strip: StripRef{Kind: StripInput, Index: input}, Field: f, Bus: bus}
}

// x32Shape is the x32 shape of v0.4: send on, pan and tap per bus pair.
var x32Shape = Shape{Colours: true, DCAMasters: true, BusLinks: true,
	Sends: SendShape{Level: true, On: true, Pan: true, Tap: true, PairScoped: true}}

// busState is testState (2 inputs, main) with buses 1, 2 (a linkable pair),
// 3 and FX bus 7, one matrix and the x32 shape.
func busState(t *testing.T, sh Shape) State {
	t.Helper()
	st, err := testState(t).WithGroups(2, 2).WithBuses([]BusInfo{
		{Index: 7, Kind: BusFX}, {Index: 1, Kind: BusMix, Linkable: true}, {Index: 2, Kind: BusMix, Linkable: true},
		{Index: 3, Kind: BusMix, Linkable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return st.WithMatrices(1).WithShape(sh)
}

func TestPan_Validation(t *testing.T) {
	for _, tc := range []struct {
		pan  float64
		isOK bool
	}{{-100, true}, {0, true}, {100, true}, {-100.5, false}, {100.01, false}, {math.NaN(), false}, {math.Inf(1), false}} {
		v, err := Pan(tc.pan)
		if (err == nil) != tc.isOK {
			t.Fatalf("Pan(%v) err = %v, want ok %v", tc.pan, err, tc.isOK)
		}
		if err != nil && !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("Pan(%v) err %v must wrap ErrInvalidValue", tc.pan, err)
		}
		if p, ok := v.Pan(); tc.isOK && (!ok || p != tc.pan || v.Kind() != KindPan) {
			t.Fatalf("Pan(%v).Pan() = %v, %v", tc.pan, p, ok)
		}
	}
	if _, ok := mustLevel(t, 3).Pan(); ok {
		t.Fatal("a level is not a pan")
	}
}

func TestTap_TokensRoundTrip(t *testing.T) {
	want := []string{"input", "preEq", "postEq", "preFader", "postFader", "group"}
	for i, tok := range want {
		tap := Tap(i + 1)
		if tap.String() != tok {
			t.Fatalf("Tap(%d) = %q, want %q", i+1, tap.String(), tok)
		}
		got, err := ParseTap(tok)
		if err != nil || got != tap {
			t.Fatalf("ParseTap(%q) = %v, %v", tok, got, err)
		}
		v, err := TapValue(tap)
		if g, ok := v.Tap(); err != nil || !ok || g != tap || v.Kind() != KindTap {
			t.Fatalf("TapValue(%v) = %v, %v", tap, v, err)
		}
	}
	if _, err := ParseTap("pre"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("ParseTap(pre) err = %v", err)
	}
	if _, err := TapValue(0); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("TapValue(0) err = %v", err)
	}
	if _, err := TapValue(TapGroup + 1); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("TapValue(7) err = %v", err)
	}
}

func TestColour_TokensRoundTrip(t *testing.T) {
	for col := range ColourCount {
		got, err := ParseColour(col.String())
		if err != nil || got != col {
			t.Fatalf("ParseColour(%q) = %v, %v", col.String(), got, err)
		}
		v, err := ColourValue(col)
		if g, ok := v.Colour(); err != nil || !ok || g != col || v.Kind() != KindColour {
			t.Fatalf("ColourValue(%v) = %v, %v", col, v, err)
		}
	}
	if Colour(0).String() != "off" || Colour(4).String() != "blue" || Colour(9).String() != "redInverted" {
		t.Fatalf("tokens: %q %q %q", Colour(0), Colour(4), Colour(9))
	}
	if _, err := ColourValue(ColourCount); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("ColourValue(16) err = %v", err)
	}
	if _, err := ParseColour("purple"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("ParseColour(purple) err = %v", err)
	}
}

func TestState_BusesSortedWithDefaults(t *testing.T) {
	st := busState(t, x32Shape)
	buses := st.Buses()
	if len(buses) != 4 || buses[0].Ref != busRef(1) || buses[3].Ref != busRef(7) {
		t.Fatalf("buses %+v", buses)
	}
	if !math.IsInf(buses[0].FaderDB, -1) {
		t.Fatalf("a new bus starts at -inf, got %v", buses[0].FaderDB)
	}
	if infos := st.BusInfos(); len(infos) != 4 || infos[3].Kind != BusFX {
		t.Fatalf("infos %+v", infos)
	}
	if _, ok := st.Strip(busRef(4)); ok {
		t.Fatal("bus 4 is absent")
	}
	if _, err := testState(t).WithBuses([]BusInfo{{Index: 1}, {Index: 1}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate bus err = %v", err)
	}
	if _, err := testState(t).WithBuses([]BusInfo{{Index: 0}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("bus 0 err = %v", err)
	}
}

func TestState_BusFieldsAndImmutability(t *testing.T) {
	st := busState(t, x32Shape)
	fader := ParamRef{Strip: busRef(2), Field: FieldFader}
	blue, _ := ColourValue(4)
	name, _ := Text("Mon 2 Keys")
	next := st
	var err error
	for _, set := range []struct {
		p ParamRef
		v Value
	}{
		{fader, mustLevel(t, -3)}, {ParamRef{Strip: busRef(2), Field: FieldMute}, Bool(true)},
		{ParamRef{Strip: busRef(2), Field: FieldName}, name}, {ParamRef{Strip: busRef(2), Field: FieldColour}, blue},
	} {
		if next, err = next.With(set.p, set.v); err != nil {
			t.Fatalf("With(%s) = %v", set.p, err)
		}
		if got, ok := next.Value(set.p); !ok || !got.Equal(set.v) {
			t.Fatalf("Value(%s) = %v, %v", set.p, got, ok)
		}
	}
	if v, _ := st.Value(fader); !v.Equal(mustLevel(t, math.Inf(-1))) {
		t.Fatal("With must not change the receiver")
	}
	if _, err := st.With(ParamRef{Strip: busRef(5), Field: FieldFader}, mustLevel(t, 0)); !errors.Is(err, ErrUnknownParam) {
		t.Fatalf("absent bus err = %v", err)
	}
	if _, ok := st.Value(ParamRef{Strip: busRef(1), Field: FieldMuteGroups}); ok {
		t.Fatal("bus membership is not modelled")
	}
}

func TestState_LinkedOnTheOddBusOfALinkablePair(t *testing.T) {
	st := busState(t, x32Shape)
	odd := ParamRef{Strip: busRef(1), Field: FieldLinked}
	next, err := st.With(odd, Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := next.Value(odd); !ok || !v.Equal(Bool(true)) {
		t.Fatalf("linked = %v, %v", v, ok)
	}
	for _, b := range []int{2, 3, 7} { // even bus; bus 3's partner 4 is absent; FX bus
		if _, ok := st.Value(ParamRef{Strip: busRef(b), Field: FieldLinked}); ok {
			t.Fatalf("bus %d must not carry linked", b)
		}
	}
	if _, ok := busState(t, Shape{}).Value(odd); ok {
		t.Fatal("without BusLinks no bus carries linked")
	}
}

func TestState_SendsPerInputAndBus(t *testing.T) {
	st := busState(t, x32Shape)
	lvl := sendRef(2, 3, FieldSendLevel)
	if v, ok := st.Value(lvl); !ok || !v.Equal(mustLevel(t, math.Inf(-1))) {
		t.Fatalf("default send level = %v, %v", v, ok)
	}
	next, err := st.With(lvl, mustLevel(t, -8))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := next.Value(lvl); !v.Equal(mustLevel(t, -8)) {
		t.Fatalf("send level = %v", v)
	}
	if v, _ := st.Value(lvl); !v.Equal(mustLevel(t, math.Inf(-1))) {
		t.Fatal("With must not change the receiver's sends")
	}
	if v, _ := next.Value(sendRef(1, 3, FieldSendLevel)); !v.Equal(mustLevel(t, math.Inf(-1))) {
		t.Fatal("another input's send changed")
	}
	if v, _ := next.Value(sendRef(2, 2, FieldSendLevel)); !v.Equal(mustLevel(t, math.Inf(-1))) {
		t.Fatal("another bus's send changed")
	}
	if sends := next.Sends(2); len(sends) != 4 || sends[2].Bus != 3 || sends[2].LevelDB != -8 {
		t.Fatalf("Sends(2) = %+v", sends)
	}
	on := sendRef(1, 1, FieldSendOn)
	if next, err = next.With(on, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if v, ok := next.Value(on); !ok || !v.Equal(Bool(true)) {
		t.Fatalf("send on = %v, %v", v, ok)
	}
}

func TestState_SendFieldsFollowTheShape(t *testing.T) {
	pan, _ := Pan(-40)
	tap, _ := TapValue(TapPreFader)
	pair := busState(t, x32Shape)
	for _, tc := range []struct {
		name string
		st   State
		p    ParamRef
		v    Value
		has  bool
	}{
		{"x32 pan on an odd bus", pair, sendRef(1, 1, FieldSendPan), pan, true},
		{"x32 pan on an even bus is the pair's, absent", pair, sendRef(1, 2, FieldSendPan), pan, false},
		{"x32 tap on an odd bus", pair, sendRef(1, 3, FieldSendTap), tap, true},
		{"x32 tap on an even bus is absent", pair, sendRef(1, 2, FieldSendTap), tap, false},
		{"pair rule does not apply to an FX bus", pair, sendRef(1, 7, FieldSendPan), pan, true},
		{"per-send pan (xair)", busState(t, Shape{Sends: SendShape{Level: true, Pan: true, Tap: true}}), sendRef(1, 2, FieldSendPan), pan, true},
		{"no send on (xair)", busState(t, Shape{Sends: SendShape{Level: true}}), sendRef(1, 1, FieldSendOn), Bool(true), false},
		{"no sends at all", busState(t, Shape{}), sendRef(1, 1, FieldSendLevel), mustLevel(t, 0), false},
		{"send to an absent bus", pair, sendRef(1, 4, FieldSendLevel), mustLevel(t, 0), false},
		{"send of an absent input", pair, sendRef(3, 1, FieldSendLevel), mustLevel(t, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := tc.st.Value(tc.p)
			next, err := tc.st.With(tc.p, tc.v)
			if ok != tc.has || (err == nil) != tc.has {
				t.Fatalf("has = %v, With err = %v; want has %v", ok, err, tc.has)
			}
			if !tc.has && !errors.Is(err, ErrUnknownParam) {
				t.Fatalf("err %v must wrap ErrUnknownParam", err)
			}
			if got, _ := next.Value(tc.p); tc.has && !got.Equal(tc.v) {
				t.Fatalf("Value = %v, want %v", got, tc.v)
			}
		})
	}
}

func TestState_SendRejectsWrongKindAndStray(t *testing.T) {
	st := busState(t, x32Shape)
	if _, err := st.With(sendRef(1, 1, FieldSendLevel), Bool(true)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("bool as level err = %v", err)
	}
	stray := ParamRef{Strip: StripRef{Kind: StripInput, Index: 1}, Field: FieldFader, Bus: 2}
	if _, ok := st.Value(stray); ok {
		t.Fatal("a bus on a non-send field is no parameter")
	}
	if _, err := st.With(stray, mustLevel(t, 0)); !errors.Is(err, ErrUnknownParam) {
		t.Fatalf("stray bus err = %v", err)
	}
	if _, err := st.With(ParamRef{Strip: MainRef, Field: FieldSendLevel, Bus: 1}, mustLevel(t, 0)); !errors.Is(err, ErrUnknownParam) {
		t.Fatalf("main has no sends, err = %v", err)
	}
}

func TestState_MatricesAndColours(t *testing.T) {
	st := busState(t, x32Shape)
	mtx := StripRef{Kind: StripMatrix, Index: 1}
	red, _ := ColourValue(1)
	next, err := st.With(ParamRef{Strip: mtx, Field: FieldColour}, red)
	if err != nil {
		t.Fatal(err)
	}
	if ms := next.Matrices(); len(ms) != 1 || ms[0].Colour != 1 || ms[0].Ref != mtx {
		t.Fatalf("matrices %+v", ms)
	}
	in1 := ParamRef{Strip: StripRef{Kind: StripInput, Index: 1}, Field: FieldColour}
	if _, err := st.With(in1, red); err != nil {
		t.Fatalf("input colour: %v", err)
	}
	if _, ok := busState(t, Shape{}).Value(in1); ok {
		t.Fatal("without Colours no strip carries a colour")
	}
	if _, ok := st.Strip(StripRef{Kind: StripMatrix, Index: 2}); ok {
		t.Fatal("matrix 2 is absent")
	}
}

func TestState_DCAMasters(t *testing.T) {
	dca1 := StripRef{Kind: StripDCA, Index: 1}
	fader := ParamRef{Strip: dca1, Field: FieldFader}
	if _, ok := busState(t, Shape{}).Value(fader); ok {
		t.Fatal("without DCA masters a DCA has only its mute")
	}
	st := busState(t, x32Shape)
	next, err := st.With(fader, mustLevel(t, -5))
	if err != nil {
		t.Fatal(err)
	}
	if next, err = next.With(ParamRef{Strip: dca1, Field: FieldMute}, Bool(true)); err != nil {
		t.Fatal(err)
	}
	strip, ok := next.Strip(dca1)
	if !ok || strip.FaderDB != -5 || !strip.Muted {
		t.Fatalf("dca 1 = %+v, %v", strip, ok)
	}
	if gs := next.DCAs(); !gs[0].Muted || gs[1].Muted {
		t.Fatalf("DCAs %+v", gs)
	}
	if _, ok := st.Value(ParamRef{Strip: StripRef{Kind: StripMuteGroup, Index: 1}, Field: FieldFader}); ok {
		t.Fatal("a mute group has no fader")
	}
}

func TestParamRef_StringNamesTheBus(t *testing.T) {
	if got := sendRef(4, 2, FieldSendLevel).String(); got != "input 4 sendLevel bus 2" {
		t.Fatalf("String = %q", got)
	}
	for _, f := range []Field{FieldSendLevel, FieldSendOn, FieldSendPan, FieldSendTap} {
		if !f.IsSend() {
			t.Fatalf("%s is a send field", f)
		}
	}
	if FieldFader.IsSend() || FieldColour.IsSend() {
		t.Fatal("fader and colour are not send fields")
	}
}
