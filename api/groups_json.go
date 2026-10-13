// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"strconv"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// effectiveMuteJSON is common.schema.json#/$defs/effectiveMute (v0.1).
type effectiveMuteJSON struct {
	Muted      bool  `json:"muted"`
	Own        bool  `json:"own"`
	MuteGroups []int `json:"muteGroups"`
	DCAs       []int `json:"dcas"`
}

// groupStateJSON is one entry of the snapshot's muteGroups or dcas list;
// v0.4 DCA masters add name, colour and faderDB.
type groupStateJSON struct {
	Index   int     `json:"index"`
	Mute    bool    `json:"mute"`
	Name    *string `json:"name,omitempty"`
	Colour  string  `json:"colour,omitempty"`
	FaderDB any     `json:"faderDB,omitempty"`
}

func effectiveJSON(e model.EffectiveMute) *effectiveMuteJSON {
	return &effectiveMuteJSON{Muted: e.Muted, Own: e.Own, MuteGroups: model.MaskMembers(e.MuteGroups), DCAs: model.MaskMembers(e.DCAs)}
}

// stripsJSON renders inputs and main with membership (when the console has
// those groups, and for main only when its console has a main membership:
// xair has none, XA section 3.2) and effective mute.
func stripsJSON(st model.State, caps model.Capabilities) []stripStateJSON {
	hasMG, hasDCA := len(st.MuteGroups()) > 0, len(st.DCAs()) > 0
	out := make([]stripStateJSON, 0, len(st.Inputs())+1)
	for _, s := range st.Strips() {
		j := stripStateJSON{Kind: s.Ref.Kind.String(), Index: s.Ref.Index, Name: s.Name, FaderDB: levelJSON(s.FaderDB), Mute: s.Muted,
			EffectiveMute: effectiveJSON(st.EffectiveMute(s.Ref))}
		if hasMG && (s.Ref.Kind != model.StripMain || caps.MainMuteGroups) {
			mg := model.MaskMembers(s.MuteGroups)
			j.MuteGroups = &mg
		}
		if hasDCA && s.Ref.Kind == model.StripInput {
			d := model.MaskMembers(s.DCAs)
			j.DCAs = &d
		}
		if caps.InputHeadamps && s.Ref.Kind == model.StripInput {
			j.Headamp = rawIndex(s.Headamp)
		}
		j.Colour = colourJSON(st, s.Ref)
		if s.Ref.Kind == model.StripInput {
			j.Sends = sendsJSON(st, s.Ref.Index)
		}
		out = append(out, j)
	}
	return out
}

// rawIndex renders a strip index as raw JSON; 0 (none) is null.
func rawIndex(n int) []byte {
	if n == 0 {
		return []byte("null")
	}
	return strconv.AppendInt(nil, int64(n), 10)
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

// eventScene is the type of the v0.1 scene-recall event.
const eventScene = "sceneRecalledAtConsole"

func sceneEventsJSON(scenes []driver.SceneRecalled) []eventJSON {
	out := make([]eventJSON, 0, len(scenes))
	for _, sc := range scenes {
		e := eventJSON{Type: eventScene, Confidence: sc.Confidence, Signals: sc.Signals}
		if sc.HighlightedScene != driver.NoScene {
			n := sc.HighlightedScene
			e.HighlightedScene = &n
		}
		if sc.LoadedScene >= 1 { // v0.2, xair: snapshots are 1-based; 0 and NoScene mean unknown
			n := sc.LoadedScene
			e.LoadedScene = &n
		}
		out = append(out, e)
	}
	return out
}

// absentInputsJSON lists the input strips the console did not answer (XA-F72).
func absentInputsJSON(c model.Capabilities) []int {
	out := make([]int, 0, max(c.AbsentInputs, 0))
	for i := 1; i <= c.AbsentInputs; i++ {
		out = append(out, c.InputCount+i)
	}
	return out
}
