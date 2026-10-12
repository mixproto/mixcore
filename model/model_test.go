// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"errors"
	"math"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func input(i int) StripRef { return StripRef{Kind: StripInput, Index: i} }

func testState(t *testing.T) State {
	t.Helper()
	inputs := []Strip{
		{Ref: input(1), Name: "Kick", FaderDB: 0},
		{Ref: input(2), Name: "Snare", FaderDB: math.Inf(-1), Muted: true},
	}
	main := Strip{Ref: MainRef, FaderDB: -5}
	s, err := NewState(inputs, &main)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return s
}

func TestLevel_Validation(t *testing.T) {
	tests := []struct {
		name    string
		db      float64
		wantErr bool
	}{
		{"finite", -12.5, false},
		{"minus infinity is off", math.Inf(-1), false},
		{"NaN rejected", math.NaN(), true},
		{"plus infinity rejected", math.Inf(1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Level(tc.db)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Level(%v) err = %v, wantErr %v", tc.db, err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("error %v must wrap ErrInvalidValue", err)
			}
			if err == nil {
				if got, ok := v.DB(); !ok || !(got == tc.db || (math.IsInf(got, -1) && math.IsInf(tc.db, -1))) {
					t.Fatalf("DB() = %v, %v", got, ok)
				}
			}
		})
	}
}

func TestText_Validation(t *testing.T) {
	if _, err := Text("Vox \xff"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid UTF-8 must be rejected, got %v", err)
	}
	if v, err := Text("Vox"); err != nil || v.Kind() != KindText {
		t.Fatalf("Text(Vox) = %v, %v", v, err)
	}
}

func TestValue_Equal(t *testing.T) {
	a, _ := Level(math.Inf(-1))
	b, _ := Level(math.Inf(-1))
	c, _ := Level(-3)
	if !a.Equal(b) || a.Equal(c) || Bool(true).Equal(Bool(false)) || !Bool(true).Equal(Bool(true)) {
		t.Fatal("Value.Equal mismatch")
	}
	if a.Equal(Bool(true)) {
		t.Fatal("values of different kinds must differ")
	}
}

func TestState_WithReturnsNewStateAndKeepsOriginal(t *testing.T) {
	s := testState(t)
	v, _ := Level(-10)
	p := ParamRef{Strip: input(1), Field: FieldFader}

	s2, err := s.With(p, v)
	if err != nil {
		t.Fatalf("With: %v", err)
	}
	if got, _ := s.Value(p); !got.Equal(mustLevel(t, 0)) {
		t.Fatalf("original state changed: %v", got)
	}
	if got, _ := s2.Value(p); !got.Equal(v) {
		t.Fatalf("new state has %v, want %v", got, v)
	}
}

func TestState_InputsReturnsCopy(t *testing.T) {
	s := testState(t)
	in := s.Inputs()
	in[0].Name = "changed"
	if st, _ := s.Strip(input(1)); st.Name != "Kick" {
		t.Fatal("Inputs() must return a copy")
	}
}

func TestState_WithRejects(t *testing.T) {
	s := testState(t)
	tests := []struct {
		name string
		p    ParamRef
		v    Value
		want error
	}{
		{"unknown strip", ParamRef{Strip: input(9), Field: FieldFader}, mustLevel(t, 0), ErrUnknownParam},
		{"unknown field", ParamRef{Strip: input(1), Field: Field(99)}, mustLevel(t, 0), ErrUnknownParam},
		{"kind mismatch", ParamRef{Strip: input(1), Field: FieldMute}, mustLevel(t, 0), ErrInvalidValue},
		{"zero value", ParamRef{Strip: input(1), Field: FieldFader}, Value{}, ErrInvalidValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.With(tc.p, tc.v); !errors.Is(err, tc.want) {
				t.Fatalf("With err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestState_AllFieldsRoundTrip(t *testing.T) {
	s := testState(t)
	name, _ := Text("Bass")
	changes := []struct {
		p ParamRef
		v Value
	}{
		{ParamRef{Strip: input(2), Field: FieldMute}, Bool(false)},
		{ParamRef{Strip: input(2), Field: FieldName}, name},
		{ParamRef{Strip: MainRef, Field: FieldFader}, mustLevel(t, math.Inf(-1))},
		{ParamRef{Strip: MainRef, Field: FieldMute}, Bool(true)},
	}
	for _, c := range changes {
		var err error
		if s, err = s.With(c.p, c.v); err != nil {
			t.Fatalf("With(%v): %v", c.p, err)
		}
		if got, ok := s.Value(c.p); !ok || !got.Equal(c.v) {
			t.Fatalf("Value(%v) = %v, want %v", c.p, got, c.v)
		}
	}
	if n := len(s.Strips()); n != 3 {
		t.Fatalf("Strips() has %d entries, want 3 (2 inputs + main)", n)
	}
}

func TestNewState_RejectsBadInputs(t *testing.T) {
	if _, err := NewState([]Strip{{Ref: input(2)}}, nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("inputs must be numbered 1..n in order, got %v", err)
	}
	if _, err := NewState([]Strip{{Ref: input(1), FaderDB: math.NaN()}}, nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("NaN fader must be rejected, got %v", err)
	}
}

func TestParse_KindsAndFields(t *testing.T) {
	for _, k := range []StripKind{StripInput, StripMain} {
		got, err := ParseStripKind(k.String())
		if err != nil || got != k {
			t.Fatalf("ParseStripKind(%q) = %v, %v", k.String(), got, err)
		}
	}
	for _, f := range []Field{FieldFader, FieldMute, FieldName} {
		got, err := ParseField(f.String())
		if err != nil || got != f {
			t.Fatalf("ParseField(%q) = %v, %v", f.String(), got, err)
		}
	}
	if _, err := ParseStripKind("bus"); !errors.Is(err, ErrUnknownParam) {
		t.Fatalf("unknown kind err = %v", err)
	}
	if _, err := ParseField("pan"); !errors.Is(err, ErrUnknownParam) {
		t.Fatalf("unknown field err = %v", err)
	}
}

func mustLevel(t *testing.T, db float64) Value {
	t.Helper()
	v, err := Level(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// v0.3: headamp strips carry gain (dB, finite) and, where read, phantom.
func TestHeadamps_Gain(t *testing.T) {
	st := headampState(t, Headamp{Index: 2, GainDB: -12}, Headamp{Index: 1, GainDB: 32.5, HasPhantom: true})
	p := ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 1}, Field: FieldGain}
	if v, ok := st.Value(p); !ok || !v.Equal(mustLevel(t, 32.5)) {
		t.Fatalf("gain = %v, %v", v, ok)
	}
	next, err := st.With(p, mustLevel(t, 35.5))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := st.Value(p); !v.Equal(mustLevel(t, 32.5)) {
		t.Fatal("With must not mutate the receiver")
	}
	if hs := next.Headamps(); len(hs) != 2 || hs[0].GainDB != 35.5 || hs[1].Index != 2 {
		t.Fatalf("headamps %+v", hs)
	}
	for _, bad := range []struct {
		p ParamRef
		v Value
	}{
		{ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 1}, Field: FieldFader}, mustLevel(t, 0)},
		{ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 0}, Field: FieldGain}, mustLevel(t, 0)},
		{p, mustLevel(t, math.Inf(-1))},
		{p, Bool(true)},
		{ParamRef{Strip: MainRef, Field: FieldGain}, mustLevel(t, 0)},
		{ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 2}, Field: FieldPhantom}, Bool(true)}, // no phantom read
		{ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 9}, Field: FieldPhantom}, Bool(true)},
	} {
		if _, err := st.With(bad.p, bad.v); err == nil {
			t.Errorf("With(%v, %v) accepted", bad.p, bad.v)
		}
	}
	if _, ok := st.Value(ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 1}, Field: FieldMute}); ok {
		t.Fatal("a headamp has no mute")
	}
	if k, err := ParseStripKind("headamp"); err != nil || k != StripHeadamp {
		t.Fatal("parse headamp")
	}
	if f, err := ParseField("gain"); err != nil || f != FieldGain || f.ValueKind() != KindLevel {
		t.Fatal("parse gain")
	}
}

// x32: a patch change brings in an AES50 headamp; its gain adds it, sorted.
func TestHeadamps_GainOfANewHeadampAddsIt(t *testing.T) {
	st := headampState(t, Headamp{Index: 1}, Headamp{Index: 40})
	next, err := st.With(ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 33}, Field: FieldGain}, mustLevel(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	hs := next.Headamps()
	if len(hs) != 3 || hs[1] != (Headamp{Index: 33, GainDB: 10}) || len(st.Headamps()) != 2 {
		t.Fatalf("headamps %+v (receiver %d)", hs, len(st.Headamps()))
	}
}

func TestHeadamps_Phantom(t *testing.T) {
	st := headampState(t, Headamp{Index: 1, HasPhantom: true})
	p := ParamRef{Strip: StripRef{Kind: StripHeadamp, Index: 1}, Field: FieldPhantom}
	next, err := st.With(p, Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := next.Value(p); !ok || !v.Equal(Bool(true)) {
		t.Fatalf("phantom %v %v", v, ok)
	}
	if v, _ := st.Value(p); !v.Equal(Bool(false)) {
		t.Fatal("With must not mutate the receiver")
	}
	if f, err := ParseField("phantom"); err != nil || f.ValueKind() != KindBool {
		t.Fatal("parse phantom")
	}
}

func TestHeadamps_InvalidStateRefused(t *testing.T) {
	st, _ := NewState(nil, nil)
	for _, hs := range [][]Headamp{{{Index: 0}}, {{Index: 1}, {Index: 1}}, {{Index: 1, GainDB: math.NaN()}}} {
		if _, err := st.WithHeadamps(hs); !errors.Is(err, ErrInvalidState) {
			t.Errorf("%+v: %v", hs, err)
		}
	}
}

// x32 /-ha: an input's headamp is an index value, 0 = none; read-only data.
func TestInputHeadampField(t *testing.T) {
	st, err := NewState([]Strip{{Ref: StripRef{Kind: StripInput, Index: 1}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := ParamRef{Strip: StripRef{Kind: StripInput, Index: 1}, Field: FieldHeadamp}
	five, _ := Index(5)
	next, err := st.With(p, five)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := next.Value(p); !ok || !v.Equal(five) {
		t.Fatalf("headamp %v %v", v, ok)
	}
	if _, err := Index(-1); !errors.Is(err, ErrInvalidValue) {
		t.Fatal("negative index accepted")
	}
	if _, err := st.With(ParamRef{Strip: MainRef, Field: FieldHeadamp}, five); err == nil {
		t.Fatal("main has no headamp field")
	}
	if n, ok := five.Index(); !ok || n != 5 || five.String() != "index 5" {
		t.Fatal("index accessors")
	}
}

func TestCapabilities_HeadampLookup(t *testing.T) {
	c := Capabilities{Headamps: []HeadampInfo{{Index: 1, Bank: BankLocal, Number: 1}, {Index: 36, Bank: BankAES50A, Number: 4}}}
	if h, ok := c.Headamp(36); !ok || h.Bank.String() != "aes50a" || h.Number != 4 {
		t.Fatalf("36: %+v %v", h, ok)
	}
	if _, ok := c.Headamp(2); ok {
		t.Fatal("2 is not described")
	}
}

func headampState(t *testing.T, hs ...Headamp) State {
	t.Helper()
	st, err := NewState(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, err = st.WithHeadamps(hs); err != nil {
		t.Fatal(err)
	}
	return st
}
