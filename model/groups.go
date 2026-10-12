// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"fmt"
	"math"
	"math/bits"
	"slices"
)

// maxGroups is the largest group number a Mask can hold.
const maxGroups = 32

// GroupState is the state of one mute group (Muted = engaged) or DCA (Muted = muted).
type GroupState struct {
	Ref   StripRef
	Muted bool
}

// EffectiveMute says whether a strip is silenced toward Main LR and why
// (design CR-01): its own mute, engaged mute groups it belongs to, muted DCAs
// it belongs to. Masks use the membership bit layout.
type EffectiveMute struct {
	Muted      bool
	Own        bool
	MuteGroups uint32
	DCAs       uint32
}

func (r StripRef) isGroup() bool { return r.Kind == StripMuteGroup || r.Kind == StripDCA }

// WithGroups returns a copy of s with muteGroups mute groups and dcas DCA
// groups, all released / unmuted.
func (s State) WithGroups(muteGroups, dcas int) State {
	s.muteGroups = make([]bool, max(muteGroups, 0))
	s.dcaMutes = make([]bool, max(dcas, 0))
	return s
}

// MuteGroups returns the mute groups in order (Muted = engaged).
func (s State) MuteGroups() []GroupState { return groupStates(StripMuteGroup, s.muteGroups) }

// DCAs returns the DCA groups in order (Muted = DCA muted).
func (s State) DCAs() []GroupState { return groupStates(StripDCA, s.dcaMutes) }

func groupStates(kind StripKind, on []bool) []GroupState {
	out := make([]GroupState, len(on))
	for i, m := range on {
		out[i] = GroupState{Ref: StripRef{Kind: kind, Index: i + 1}, Muted: m}
	}
	return out
}

func (s State) groupList(kind StripKind) []bool {
	if kind == StripMuteGroup {
		return s.muteGroups
	}
	return s.dcaMutes
}

func groupAt(list []bool, index int) (bool, bool) {
	if index < 1 || index > len(list) {
		return false, false
	}
	return list[index-1], true
}

// withGroup sets the mute of a group strip in a copy of s.
func (s State) withGroup(p ParamRef, v Value) (State, error) {
	list := s.groupList(p.Strip.Kind)
	if _, ok := groupAt(list, p.Strip.Index); !ok || p.Field != FieldMute {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	on, ok := v.Bool()
	if !ok {
		return s, fmt.Errorf("%s needs on/off: %w", p, ErrInvalidValue)
	}
	list = slices.Clone(list)
	list[p.Strip.Index-1] = on
	if p.Strip.Kind == StripMuteGroup {
		s.muteGroups = list
	} else {
		s.dcaMutes = list
	}
	return s, nil
}

// EffectiveMute returns the effective mute of an input or main strip toward
// Main LR; group strips and unknown refs give the zero value.
func (s State) EffectiveMute(ref StripRef) EffectiveMute {
	if ref.isGroup() {
		return EffectiveMute{}
	}
	st, ok := s.Strip(ref)
	if !ok {
		return EffectiveMute{}
	}
	e := EffectiveMute{Own: st.Muted, MuteGroups: activeMembers(st.MuteGroups, s.muteGroups), DCAs: activeMembers(st.DCAs, s.dcaMutes)}
	e.Muted = e.Own || e.MuteGroups != 0 || e.DCAs != 0
	return e
}

// activeMembers keeps the bits of member whose group is on.
func activeMembers(member uint32, on []bool) uint32 {
	var out uint32
	for i, o := range on {
		if o && i < maxGroups && member&(1<<i) != 0 {
			out |= 1 << i
		}
	}
	return out
}

// MaskMembers lists the group numbers set in m, ascending.
func MaskMembers(m uint32) []int {
	out := make([]int, 0, bits.OnesCount32(m))
	for i := range maxGroups {
		if m&(1<<i) != 0 {
			out = append(out, i+1)
		}
	}
	return out
}

// MaskOf builds a mask from group numbers 1..limit, each at most once.
func MaskOf(groups []int, limit int) (uint32, error) {
	var m uint32
	for _, g := range groups {
		if g < 1 || g > min(limit, maxGroups) || m&(1<<(g-1)) != 0 {
			return 0, fmt.Errorf("group %d (1..%d, once each): %w", g, limit, ErrInvalidValue)
		}
		m |= 1 << (g - 1)
	}
	return m, nil
}

// WithHeadamps returns a copy of s with headamps 1..n at gains (dB). The
// slice is copied; n = len(gains).
func (s State) WithHeadamps(gains []float64) State {
	s.headamps = slices.Clone(gains)
	return s
}

// Headamps returns the headamp strips in order (Ref and GainDB set).
func (s State) Headamps() []Strip {
	out := make([]Strip, len(s.headamps))
	for i, g := range s.headamps {
		out[i] = Strip{Ref: StripRef{Kind: StripHeadamp, Index: i + 1}, GainDB: g}
	}
	return out
}

// withHeadamp sets the gain of a headamp strip in a copy of s.
func (s State) withHeadamp(p ParamRef, v Value) (State, error) {
	if p.Strip.Kind != StripHeadamp || p.Field != FieldGain || p.Strip.Index < 1 || p.Strip.Index > len(s.headamps) {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	db, ok := v.DB()
	if !ok || math.IsInf(db, 0) {
		return s, fmt.Errorf("%s needs a finite level: %w", p, ErrInvalidValue)
	}
	s.headamps = slices.Clone(s.headamps)
	s.headamps[p.Strip.Index-1] = db
	return s, nil
}
