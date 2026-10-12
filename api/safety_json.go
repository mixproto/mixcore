// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"math"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/model"
)

// JSON of the v0.3 phone safety additions (gesture-result.schema.json,
// state.schema.json).

type windowJSON struct {
	StartDB     any      `json:"startDB"`
	WallUpDB    *float64 `json:"wallUpDB,omitempty"`
	CapUpDB     *float64 `json:"capUpDB,omitempty"`
	CapDownDB   *float64 `json:"capDownDB,omitempty"`
	CeilingDB   *float64 `json:"ceilingDB,omitempty"`
	OpensByRest bool     `json:"opensByRest"`
}

type gestureResultJSON struct {
	Version int         `json:"version"`
	State   string      `json:"state"`
	Value   any         `json:"value,omitempty"`
	Written bool        `json:"written"`
	Window  *windowJSON `json:"window,omitempty"`
}

func ptr(v float64) *float64 { return &v }

func resultJSON(r console.Result) gestureResultJSON {
	out := gestureResultJSON{Version: contractVersion, State: r.State.String(), Written: r.Written}
	if db, ok := r.Value.DB(); ok {
		out.Value = levelJSON(db)
	}
	if w := r.Window; w != nil {
		j := &windowJSON{StartDB: levelJSON(w.Start), OpensByRest: w.OpensByRest}
		if w.HasWall {
			j.WallUpDB = ptr(w.WallUp)
		}
		if w.HasCap {
			j.CapUpDB, j.CapDownDB = ptr(w.CapUp), ptr(w.CapDown)
		}
		if w.HasCeiling && !math.IsInf(w.Ceiling, 0) {
			j.CeilingDB = ptr(w.Ceiling)
		}
		out.Window = j
	}
	return out
}

type headampJSON struct {
	Index  int     `json:"index"`
	GainDB float64 `json:"gainDB"`
}

type headampRangeJSON struct {
	MinDB  float64 `json:"minDB"`
	MaxDB  float64 `json:"maxDB"`
	StepDB float64 `json:"stepDB"`
}

func headampsJSON(st model.State, c model.Capabilities) ([]headampJSON, *headampRangeJSON) {
	hs := st.Headamps()
	if len(hs) == 0 {
		return nil, nil
	}
	out := make([]headampJSON, 0, len(hs))
	for _, h := range hs {
		out = append(out, headampJSON{Index: h.Ref.Index, GainDB: h.GainDB})
	}
	return out, &headampRangeJSON{MinDB: c.HeadampMinDB, MaxDB: c.HeadampMaxDB, StepDB: c.HeadampStepDB}
}

const eventUnlockEnded = "gainUnlockEnded"

func unlockEventsJSON(evs []console.UnlockEvent) []eventJSON {
	out := make([]eventJSON, 0, len(evs))
	for _, e := range evs {
		i := e.Headamp
		out = append(out, eventJSON{Type: eventUnlockEnded, Index: &i, Reason: e.Reason})
	}
	return out
}

// safety turns the config's safety object into console defaults (parseConfig
// validated it).
func (c configJSON) safety() console.SafetyConfig {
	m, _ := c.Safety.monitor()
	return console.SafetyConfig{Client: console.ClientSafety{SlipGuard: c.Safety.SlipGuard, Monitor: m}, ShowMode: c.Safety.ShowMode}
}
