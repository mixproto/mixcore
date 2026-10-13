// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import "fmt"

// MuteBasis says where an effective mute comes from (contracts/core-api
// common.schema.json effectiveMute.basis).
type MuteBasis uint8

// Mute bases. The zero value is BasisDerived, the x32 and xair rule.
const (
	// BasisDerived: computed from the strip's own mute, its membership and
	// the group states, every one of them known.
	BasisDerived MuteBasis = iota
	// BasisConsole: the console reported the strip's effective mute itself.
	BasisConsole
	// BasisPartial: an input of the rule is unknown, so the effective mute
	// is unknown (Known false), never reported as live.
	BasisPartial
)

var basisNames = [...]string{"derived", "console", "partial"}

func (b MuteBasis) String() string {
	if int(b) < len(basisNames) {
		return basisNames[b]
	}
	return fmt.Sprintf("MuteBasis(%d)", b)
}

// Values of FieldConsoleMute: the console's own report of a strip's
// effective mute (wing $mute, docs/protocols/wing.md WG-F24, section 3.2).
const (
	ConsoleMuteNone    = 0 // the console reports none (x32, xair)
	ConsoleMuteOff     = 1 // reported: not muted
	ConsoleMuteOn      = 2 // reported: muted
	ConsoleMuteUnknown = 3 // reported with a value whose meaning is unknown (wing $mute 2, item 12)
	consoleMuteMax     = ConsoleMuteUnknown
)

// MuteRules are what the effective-mute rule must know about a console. The
// zero value is the x32 and xair rule: memberships known, a muted DCA
// silences its members.
type MuteRules struct {
	// MuteGroupMembershipUnknown: strips' mute-group membership cannot be
	// read (wing, item 11); an engaged mute group may silence any strip.
	MuteGroupMembershipUnknown bool
	// DCAMembershipUnknown: strips' DCA membership cannot be read.
	DCAMembershipUnknown bool
	// DCAMuteSilent: a muted DCA does not mute its members (wing with
	// /cfg/dcamgrp = 0, wing.md section 3.2).
	DCAMuteSilent bool
}

// WithMuteRules returns a copy of s with the given rules.
func (s State) WithMuteRules(r MuteRules) State {
	s.rules = r
	return s
}

// MuteRules returns the rules of s.
func (s State) MuteRules() MuteRules { return s.rules }

// effectiveOf applies the rule to one strip (design CR-01, SR-04): the
// strip's own mute is definite; then the console's own report; then the
// derived value, unknown when an engaged group's membership is unknown.
func (s State) effectiveOf(st Strip) EffectiveMute {
	e := EffectiveMute{Own: st.Muted}
	if !s.rules.MuteGroupMembershipUnknown { // an unknown membership's mask means nothing
		e.MuteGroups = activeMembers(st.MuteGroups, s.muteGroups)
	}
	if !s.rules.DCAMuteSilent && !s.rules.DCAMembershipUnknown {
		e.DCAs = activeMembers(st.DCAs, s.dcaMutes)
	}
	switch {
	case e.Own:
		e.Muted, e.Known = true, true
	case st.ConsoleMute == ConsoleMuteOn || st.ConsoleMute == ConsoleMuteOff:
		e.Muted, e.Known, e.Basis = st.ConsoleMute == ConsoleMuteOn, true, BasisConsole
	case st.ConsoleMute == ConsoleMuteUnknown:
		e.Basis = BasisPartial
	case e.MuteGroups != 0 || e.DCAs != 0:
		e.Muted, e.Known = true, true
	case s.rules.MuteGroupMembershipUnknown && anyOn(s.muteGroups),
		s.rules.DCAMembershipUnknown && !s.rules.DCAMuteSilent && anyOn(s.dcaMutes):
		e.Basis = BasisPartial
	default:
		e.Known = true
	}
	if e.Own && st.ConsoleMute == ConsoleMuteOn {
		e.Basis = BasisConsole
	}
	return e
}

func anyOn(on []bool) bool {
	for _, o := range on {
		if o {
			return true
		}
	}
	return false
}
