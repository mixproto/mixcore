// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mixproto/mixcore/model"
)

// JSON of the v0.4 additions: buses, sends, matrices, DCA masters and
// colours (state.schema.json, common.schema.json).

// Send field scopes of sendFields.pan and sendFields.tap.
const (
	scopeSend = "send"
	scopePair = "pair"
)

// outputJSON is one entry of buses or matrices.
type outputJSON struct {
	Index   int    `json:"index"`
	Kind    string `json:"kind,omitempty"` // buses only: mix or fx
	Name    string `json:"name"`
	Colour  string `json:"colour,omitempty"`
	FaderDB any    `json:"faderDB"`
	Mute    bool   `json:"mute"`
	Linked  *bool  `json:"linked,omitempty"` // the odd bus of a linkable pair
}

// sendJSON is one send of an input strip.
type sendJSON struct {
	Bus     int      `json:"bus"`
	LevelDB any      `json:"levelDB"`
	On      *bool    `json:"on,omitempty"`
	Pan     *float64 `json:"pan,omitempty"`
	Tap     string   `json:"tap,omitempty"`
}

type sendFieldsJSON struct {
	On  bool   `json:"on,omitempty"`
	Pan string `json:"pan,omitempty"`
	Tap string `json:"tap,omitempty"`
}

type tapLabelJSON struct {
	Tap   string `json:"tap"`
	Label string `json:"label"`
}

// colourJSON is a strip's colour token, "" when the state has no colours.
func colourJSON(st model.State, ref model.StripRef) string {
	v, ok := st.Value(model.ParamRef{Strip: ref, Field: model.FieldColour})
	if !ok {
		return ""
	}
	c, _ := v.Colour()
	return c.String()
}

// busesJSON lists the buses with their kind and, on the odd bus of a
// linkable pair, the link.
func busesJSON(st model.State) []outputJSON {
	infos := st.BusInfos()
	out := make([]outputJSON, 0, len(infos))
	for i, b := range st.Buses() {
		j := outputJSON{Index: b.Ref.Index, Kind: infos[i].Kind.String(), Name: b.Name, Colour: colourJSON(st, b.Ref),
			FaderDB: levelJSON(b.FaderDB), Mute: b.Muted}
		if v, ok := st.Value(model.ParamRef{Strip: b.Ref, Field: model.FieldLinked}); ok {
			on, _ := v.Bool()
			j.Linked = &on
		}
		out = append(out, j)
	}
	return out
}

func matricesJSON(st model.State) []outputJSON {
	ms := st.Matrices()
	out := make([]outputJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, outputJSON{Index: m.Ref.Index, Name: m.Name, Colour: colourJSON(st, m.Ref), FaderDB: levelJSON(m.FaderDB), Mute: m.Muted})
	}
	return out
}

// sendsJSON renders input's sends with the fields the state has for each.
func sendsJSON(st model.State, input int) []sendJSON {
	sends := st.Sends(input)
	if len(sends) == 0 || !st.Shape().Sends.Level {
		return nil
	}
	in := model.StripRef{Kind: model.StripInput, Index: input}
	out := make([]sendJSON, 0, len(sends))
	for _, s := range sends {
		j := sendJSON{Bus: s.Bus, LevelDB: levelJSON(s.LevelDB)}
		has := func(f model.Field) bool {
			_, ok := st.Value(model.ParamRef{Strip: in, Field: f, Bus: s.Bus})
			return ok
		}
		if has(model.FieldSendOn) {
			on := s.On
			j.On = &on
		}
		if has(model.FieldSendPan) {
			pan := s.Pan
			j.Pan = &pan
		}
		if has(model.FieldSendTap) {
			j.Tap = s.Tap.String()
		}
		out = append(out, j)
	}
	return out
}

// sendFieldsOf describes which send fields the console has (nil: no sends).
func sendFieldsOf(st model.State) *sendFieldsJSON {
	sh := st.Shape().Sends
	if !sh.Level || len(st.Buses()) == 0 {
		return nil
	}
	scope := scopeSend
	if sh.PairScoped {
		scope = scopePair
	}
	f := &sendFieldsJSON{On: sh.On}
	if sh.Pan {
		f.Pan = scope
	}
	if sh.Tap {
		f.Tap = scope
	}
	return f
}

// sendTapsJSON lists the console's words for the taps (CR-16).
func sendTapsJSON(st model.State, c model.Capabilities) []tapLabelJSON {
	if !st.Shape().Sends.Tap {
		return nil
	}
	out := make([]tapLabelJSON, 0, model.TapCount)
	for i, label := range c.TapLabels {
		out = append(out, tapLabelJSON{Tap: model.Tap(i + 1).String(), Label: label})
	}
	return out
}

// dcasJSON renders the DCAs with their masters where the console has them.
func dcasJSON(st model.State) []groupStateJSON {
	out := groupStatesJSON(st.DCAs())
	if !st.Shape().DCAMasters {
		return out
	}
	for i := range out {
		d, _ := st.Strip(model.StripRef{Kind: model.StripDCA, Index: out[i].Index})
		name := d.Name
		out[i].Name, out[i].Colour, out[i].FaderDB = &name, colourJSON(st, d.Ref), levelJSON(d.FaderDB)
	}
	return out
}

// errNoBus: a send field without its bus, or a bus on another field.
var errNoBus = errors.New("bus goes with sendLevel, sendOn, sendPan and sendTap, and only with them")

// parseBus checks the bus member of a param (v0.4).
func parseBus(f model.Field, bus int) error {
	if f.IsSend() != (bus != 0) || bus < 0 || bus > maxRoleBus {
		return fmt.Errorf("%s bus %d: %w", f, bus, errNoBus)
	}
	return nil
}

// parseV04Value parses the value of a v0.4 field; handled is false for
// other fields. A pan outside -100..+100 is ERR_INVALID_VALUE.
func parseV04Value(f model.Field, raw json.RawMessage) (v model.Value, handled bool, err error) {
	switch f {
	case model.FieldSendLevel:
		v, err = parseLevel(raw)
	case model.FieldSendOn, model.FieldLinked:
		var b bool
		if err = json.Unmarshal(raw, &b); err == nil {
			v = model.Bool(b)
		}
	case model.FieldSendPan:
		var p float64
		if err = json.Unmarshal(raw, &p); err != nil {
			return v, true, codeErr(CodeInvalidCommand, err)
		}
		if v, err = model.Pan(p); err != nil {
			return v, true, codeErr(CodeInvalidValue, err)
		}
	case model.FieldSendTap:
		v, err = parseToken(raw, func(s string) (model.Value, error) {
			t, err := model.ParseTap(s)
			if err != nil {
				return model.Value{}, err
			}
			return model.TapValue(t)
		})
	case model.FieldColour:
		v, err = parseToken(raw, func(s string) (model.Value, error) {
			c, err := model.ParseColour(s)
			if err != nil {
				return model.Value{}, err
			}
			return model.ColourValue(c)
		})
	default:
		return v, false, nil
	}
	if err != nil {
		return v, true, codeErr(CodeInvalidCommand, fmt.Errorf("%s value: %w", f, err))
	}
	return v, true, nil
}

func parseToken(raw json.RawMessage, parse func(string) (model.Value, error)) (model.Value, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return model.Value{}, err
	}
	return parse(s)
}

// v04ValueJSON renders a pan, tap or colour value; ok is false for other kinds.
func v04ValueJSON(v model.Value) (any, bool) {
	if p, ok := v.Pan(); ok {
		return p, true
	}
	if t, ok := v.Tap(); ok {
		return t.String(), true
	}
	if c, ok := v.Colour(); ok {
		return c.String(), true
	}
	return nil, false
}
