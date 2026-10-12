// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"errors"
	"slices"
	"testing"
)

func groupRef(k StripKind, i int) StripRef { return StripRef{Kind: k, Index: i} }

func groupState(t *testing.T) State {
	t.Helper()
	return testState(t).WithGroups(6, 8)
}

func TestMask_ValueAndList(t *testing.T) {
	v := Mask(0b100010)
	if m, ok := v.Mask(); !ok || m != 0b100010 || v.Kind() != KindMask {
		t.Fatalf("Mask = %v %v", m, ok)
	}
	if got := MaskMembers(0b100010); !slices.Equal(got, []int{2, 6}) {
		t.Fatalf("MaskMembers = %v", got)
	}
	if m, err := MaskOf([]int{2, 6}, 6); err != nil || m != 0b100010 {
		t.Fatalf("MaskOf = %v %v", m, err)
	}
	for _, bad := range [][]int{{0}, {7}, {2, 2}} {
		if _, err := MaskOf(bad, 6); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("MaskOf(%v) err = %v", bad, err)
		}
	}
	if v.String() != "mask 100010" {
		t.Fatalf("String = %q", v.String())
	}
}

func TestGroups_StateAndValues(t *testing.T) {
	s := groupState(t)
	if n := len(s.MuteGroups()); n != 6 || len(s.DCAs()) != 8 {
		t.Fatalf("%d mute groups, %d DCAs", n, len(s.DCAs()))
	}
	mg2 := ParamRef{Strip: groupRef(StripMuteGroup, 2), Field: FieldMute}
	next, err := s.With(mg2, Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := next.Value(mg2); !v.Equal(Bool(true)) {
		t.Fatalf("mute group 2 = %v", v)
	}
	if v, _ := s.Value(mg2); !v.Equal(Bool(false)) {
		t.Fatal("With must not change the receiver")
	}
	for _, bad := range []ParamRef{
		{Strip: groupRef(StripMuteGroup, 7), Field: FieldMute},
		{Strip: groupRef(StripDCA, 0), Field: FieldMute},
		{Strip: groupRef(StripDCA, 1), Field: FieldFader},
	} {
		if _, err := next.With(bad, Bool(true)); err == nil {
			t.Fatalf("With(%v) must fail", bad)
		}
	}
	if _, err := next.With(ParamRef{Strip: input(1), Field: FieldMuteGroups}, Bool(true)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("membership needs a mask: %v", err)
	}
	if k, err := ParseStripKind("muteGroup"); err != nil || k != StripMuteGroup || StripDCA.String() != "dca" {
		t.Fatalf("parse muteGroup = %v %v", k, err)
	}
	if f, err := ParseField("dcas"); err != nil || f != FieldDCAs || FieldMuteGroups.String() != "muteGroups" {
		t.Fatalf("parse dcas = %v %v", f, err)
	}
}

// CR-01: own mute OR an engaged member mute group OR a muted member DCA.
func TestEffectiveMute(t *testing.T) {
	s := groupState(t)
	set := func(p ParamRef, v Value) {
		t.Helper()
		var err error
		if s, err = s.With(p, v); err != nil {
			t.Fatal(err)
		}
	}
	kick := input(1)
	set(ParamRef{Strip: kick, Field: FieldMuteGroups}, Mask(0b10))
	set(ParamRef{Strip: kick, Field: FieldDCAs}, Mask(0b1))
	if e := s.EffectiveMute(kick); e.Muted || e.Own || e.MuteGroups != 0 || e.DCAs != 0 {
		t.Fatalf("nothing engaged: %+v", e)
	}
	set(ParamRef{Strip: groupRef(StripMuteGroup, 2), Field: FieldMute}, Bool(true))
	set(ParamRef{Strip: groupRef(StripMuteGroup, 3), Field: FieldMute}, Bool(true)) // kick is not in group 3
	if e := s.EffectiveMute(kick); !e.Muted || e.Own || e.MuteGroups != 0b10 {
		t.Fatalf("group 2 engaged: %+v", e)
	}
	set(ParamRef{Strip: groupRef(StripDCA, 1), Field: FieldMute}, Bool(true))
	set(ParamRef{Strip: kick, Field: FieldMute}, Bool(true))
	if e := s.EffectiveMute(kick); !e.Muted || !e.Own || e.DCAs != 0b1 || e.MuteGroups != 0b10 {
		t.Fatalf("all sources: %+v", e)
	}
	if e := s.EffectiveMute(input(2)); !e.Muted || !e.Own || e.MuteGroups != 0 {
		t.Fatalf("snare, own mute only: %+v", e)
	}
	if e := s.EffectiveMute(groupRef(StripDCA, 1)); e != (EffectiveMute{}) {
		t.Fatalf("groups have no effective mute: %+v", e)
	}
}
