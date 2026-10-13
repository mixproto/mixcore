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
	CapUpDB     *float64 `json:"capUpDB,omitempty"`
	CapDownDB   *float64 `json:"capDownDB,omitempty"`
	CeilingDB   *float64 `json:"ceilingDB,omitempty"`
	OpensByRest bool     `json:"opensByRest"` // deprecated in v0.5: always false, removed in v0.6
}

type gestureResultJSON struct {
	Version int         `json:"version"`
	State   string      `json:"state"`
	Value   any         `json:"value,omitempty"`
	Written bool        `json:"written"`
	Window  *windowJSON `json:"window,omitempty"`
	// PinRecord: v0.5, lockShowMode only: the record to store (CC-12).
	PinRecord string `json:"pinRecord,omitempty"`
}

func ptr(v float64) *float64 { return &v }

func resultJSON(r console.Result) gestureResultJSON {
	out := gestureResultJSON{Version: contractVersion, State: r.State.String(), Written: r.Written, PinRecord: r.PinRecord}
	if db, ok := r.Value.DB(); ok {
		out.Value = levelJSON(db)
	}
	if w := r.Window; w != nil {
		j := &windowJSON{StartDB: levelJSON(w.Start)}
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

// headampJSON is one snapshot headamp with its own law and label (v0.3).
type headampJSON struct {
	Index      int     `json:"index"`
	GainDB     float64 `json:"gainDB"`
	MinDB      float64 `json:"minDB"`
	MaxDB      float64 `json:"maxDB"`
	WriteMaxDB float64 `json:"writeMaxDB"`
	StepDB     float64 `json:"stepDB"`
	Writable   bool    `json:"writable"`
	Bank       string  `json:"bank"`
	Number     int     `json:"number"`
	Phantom    *bool   `json:"phantom,omitempty"`
}

// headampsJSON lists the headamps of the state with their description. A
// headamp the capabilities do not describe is left out: no law is invented.
func headampsJSON(st model.State, c model.Capabilities) []headampJSON {
	hs := st.Headamps()
	out := make([]headampJSON, 0, len(hs))
	for _, h := range hs {
		info, ok := c.Headamp(h.Index)
		if !ok {
			continue
		}
		j := headampJSON{Index: h.Index, GainDB: h.GainDB, MinDB: info.MinDB, MaxDB: info.MaxDB, WriteMaxDB: info.WriteMaxDB, StepDB: info.StepDB,
			Writable: info.Writable, Bank: info.Bank.String(), Number: info.Number}
		if h.HasPhantom {
			on := h.Phantom
			j.Phantom = &on
		}
		out = append(out, j)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	return console.SafetyConfig{Client: console.ClientSafety{Monitor: m}, ShowMode: c.Safety.ShowMode, // slipGuard: ignored since v0.5
		ShowModeLock: c.showModeLock()}
}
