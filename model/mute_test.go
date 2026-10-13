// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import "testing"

// muteState is a state with input 1 in mute group 2 and DCA 3 (as far as
// the driver knows), group 2 engaged or not, DCA 3 muted or not.
func muteState(t *testing.T, own, groupOn, dcaOn bool, console int, rules MuteRules) State {
	t.Helper()
	in := Strip{Ref: StripRef{Kind: StripInput, Index: 1}, Muted: own, MuteGroups: 1 << 1, DCAs: 1 << 2}
	st, err := NewState([]Strip{in}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st = st.WithGroups(4, 4).WithMuteRules(rules)
	set := func(p ParamRef, v Value) {
		if st, err = st.With(p, v); err != nil {
			t.Fatal(err)
		}
	}
	set(ParamRef{Strip: StripRef{Kind: StripMuteGroup, Index: 2}, Field: FieldMute}, Bool(groupOn))
	set(ParamRef{Strip: StripRef{Kind: StripDCA, Index: 3}, Field: FieldMute}, Bool(dcaOn))
	if console != ConsoleMuteNone {
		v, _ := Index(console)
		set(ParamRef{Strip: in.Ref, Field: FieldConsoleMute}, v)
	}
	return st
}

// TestEffectiveMute_TriState: a definite value only when every input is
// known; the console's own report wins (wing $mute, WG-F24); a mute group
// with unknown membership makes an unmuted strip unknown, never live.
func TestEffectiveMute_TriState(t *testing.T) {
	unknownMG := MuteRules{MuteGroupMembershipUnknown: true}
	cases := []struct {
		name                 string
		own, group, dca      bool
		console              int
		rules                MuteRules
		wantKnown, wantMuted bool
		wantBasis            MuteBasis
	}{
		{"x32 rule: nothing engaged", false, false, false, ConsoleMuteNone, MuteRules{}, true, false, BasisDerived},
		{"x32 rule: group engaged", false, true, false, ConsoleMuteNone, MuteRules{}, true, true, BasisDerived},
		{"x32 rule: dca muted", false, false, true, ConsoleMuteNone, MuteRules{}, true, true, BasisDerived},
		{"dca mute silences nobody (dcamgrp 0)", false, false, true, ConsoleMuteNone, MuteRules{DCAMuteSilent: true}, true, false, BasisDerived},
		{"unknown membership, group engaged: null", false, true, false, ConsoleMuteNone, unknownMG, false, false, BasisPartial},
		{"unknown membership, own mute: true", true, true, false, ConsoleMuteNone, unknownMG, true, true, BasisDerived},
		{"unknown membership, nothing engaged: false", false, false, false, ConsoleMuteNone, unknownMG, true, false, BasisDerived},
		{"unknown dca membership, dca muted: null", false, false, true, ConsoleMuteNone, MuteRules{DCAMembershipUnknown: true}, false, false, BasisPartial},
		{"console says muted", false, true, false, ConsoleMuteOn, unknownMG, true, true, BasisConsole},
		{"console says not muted", false, true, false, ConsoleMuteOff, unknownMG, true, false, BasisConsole},
		{"console value not understood: null", false, true, false, ConsoleMuteUnknown, unknownMG, false, false, BasisPartial},
		{"console value not understood, own mute: true", true, false, false, ConsoleMuteUnknown, unknownMG, true, true, BasisDerived},
	}
	for _, tc := range cases {
		e := muteState(t, tc.own, tc.group, tc.dca, tc.console, tc.rules).EffectiveMute(StripRef{Kind: StripInput, Index: 1})
		if e.Known != tc.wantKnown || (e.Known && e.Muted != tc.wantMuted) || e.Basis != tc.wantBasis {
			t.Errorf("%s: %+v", tc.name, e)
		}
	}
}

func TestConsoleMute_FieldIsInternal(t *testing.T) {
	if _, err := ParseField(FieldConsoleMute.String()); err == nil {
		t.Fatal("consoleMute must not be a field clients can name")
	}
	v, _ := Index(4)
	st := muteState(t, false, false, false, ConsoleMuteNone, MuteRules{})
	if _, err := st.With(ParamRef{Strip: StripRef{Kind: StripInput, Index: 1}, Field: FieldConsoleMute}, v); err == nil {
		t.Fatal("console mute 4 is not a value")
	}
}
