// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/model"
)

// effectiveMuteJSON is common.schema.json#/$defs/effectiveMute (v0.1).
type effectiveMuteJSON struct {
	Muted      bool  `json:"muted"`
	Own        bool  `json:"own"`
	MuteGroups []int `json:"muteGroups"`
	DCAs       []int `json:"dcas"`
}

// groupStateJSON is one entry of the snapshot's muteGroups or dcas list.
type groupStateJSON struct {
	Index int  `json:"index"`
	Mute  bool `json:"mute"`
}

func effectiveJSON(e model.EffectiveMute) *effectiveMuteJSON {
	return &effectiveMuteJSON{Muted: e.Muted, Own: e.Own, MuteGroups: model.MaskMembers(e.MuteGroups), DCAs: model.MaskMembers(e.DCAs)}
}

// stripsJSON renders inputs and main with membership (when the console has
// those groups) and effective mute.
func stripsJSON(st model.State) []stripStateJSON {
	hasMG, hasDCA := len(st.MuteGroups()) > 0, len(st.DCAs()) > 0
	out := make([]stripStateJSON, 0, len(st.Inputs())+1)
	for _, s := range st.Strips() {
		j := stripStateJSON{Kind: s.Ref.Kind.String(), Index: s.Ref.Index, Name: s.Name, FaderDB: levelJSON(s.FaderDB), Mute: s.Muted,
			EffectiveMute: effectiveJSON(st.EffectiveMute(s.Ref))}
		if hasMG {
			mg := model.MaskMembers(s.MuteGroups)
			j.MuteGroups = &mg
		}
		if hasDCA && s.Ref.Kind == model.StripInput {
			d := model.MaskMembers(s.DCAs)
			j.DCAs = &d
		}
		out = append(out, j)
	}
	return out
}

func groupStatesJSON(gs []model.GroupState) []groupStateJSON {
	out := make([]groupStateJSON, 0, len(gs))
	for _, g := range gs {
		out = append(out, groupStateJSON{Index: g.Ref.Index, Mute: g.Muted})
	}
	return out
}

// effectiveChangesJSON renders effective-mute changes as diff entries with
// field effectiveMute.
func effectiveChangesJSON(evs []console.EffectiveChange) []changeJSON {
	out := make([]changeJSON, 0, len(evs))
	for _, e := range evs {
		p := paramToJSON(model.ParamRef{Strip: e.Strip})
		p.Field = fieldEffective
		out = append(out, changeJSON{Param: p, Value: effectiveJSON(e.Mute), Origin: originToJSON(e.Origin)})
	}
	return out
}
