// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// JSON shapes of contracts/core-api v0.
const (
	contractVersion = 0
	maxJSONBytes    = 64 << 10 // larger input is rejected before parsing
	minusInf        = "-inf"
	cmdSet          = "set"
	cmdHoldBegin    = "holdBegin"
	cmdHoldEnd      = "holdEnd"
	familyX32       = "x32"
	maxClientIDLen  = 128
)

var errVersion = errors.New("unsupported contract version")

type configJSON struct {
	Version *int   `json:"version"`
	Family  string `json:"family"`
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
	Meters  struct {
		Inputs bool `json:"inputs"`
	} `json:"meters"`
}

type stripJSON struct {
	Kind  string `json:"kind"`
	Index int    `json:"index,omitempty"`
}

type paramJSON struct {
	Strip stripJSON `json:"strip"`
	Field string    `json:"field"`
}

type originJSON struct {
	Kind     string `json:"kind"`
	ClientID string `json:"clientId,omitempty"`
	Seq      uint64 `json:"seq,omitempty"`
}

type commandJSON struct {
	Version *int            `json:"version"`
	Type    string          `json:"type"`
	Param   paramJSON       `json:"param"`
	Value   json.RawMessage `json:"value,omitempty"`
	Origin  originJSON      `json:"origin"`
}

// command is a parsed, validated Apply input.
type command struct {
	kind   string
	param  model.ParamRef
	value  model.Value
	origin driver.Origin
}

func decodeStrict(data string, v any) error {
	if len(data) > maxJSONBytes {
		return fmt.Errorf("input of %d bytes exceeds %d", len(data), maxJSONBytes)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON object")
	}
	return nil
}

func parseConfig(data string) (configJSON, error) {
	var c configJSON
	if err := decodeStrict(data, &c); err != nil {
		return c, codeErr(CodeInvalidConfig, err)
	}
	if c.Version == nil || *c.Version != contractVersion {
		return c, codeErr(CodeInvalidConfig, errVersion)
	}
	if c.Family != familyX32 {
		return c, codeErr(CodeInvalidConfig, fmt.Errorf("unknown family %q", c.Family))
	}
	if _, err := netip.ParseAddr(c.Host); err != nil {
		return c, codeErr(CodeInvalidConfig, fmt.Errorf("host must be an IP address: %w", err))
	}
	if c.Port < 0 || c.Port > math.MaxUint16 {
		return c, codeErr(CodeInvalidConfig, fmt.Errorf("port %d", c.Port))
	}
	return c, nil
}

func parseCommand(data string) (command, error) {
	var c commandJSON
	if err := decodeStrict(data, &c); err != nil {
		return command{}, codeErr(CodeInvalidCommand, err)
	}
	if c.Version == nil || *c.Version != contractVersion {
		return command{}, codeErr(CodeInvalidCommand, errVersion)
	}
	if c.Type != cmdSet && c.Type != cmdHoldBegin && c.Type != cmdHoldEnd {
		return command{}, codeErr(CodeInvalidCommand, fmt.Errorf("type %q", c.Type))
	}
	p, err := parseParam(c.Param)
	if err != nil {
		return command{}, err
	}
	o, err := parseOrigin(c.Origin)
	if err != nil {
		return command{}, err
	}
	cmd := command{kind: c.Type, param: p, origin: o}
	if c.Type != cmdSet {
		if len(c.Value) != 0 {
			return command{}, codeErr(CodeInvalidCommand, errors.New("hold commands carry no value"))
		}
		return cmd, nil
	}
	if cmd.value, err = parseValue(p.Field, c.Value); err != nil {
		return command{}, err
	}
	return cmd, nil
}

func parseParam(p paramJSON) (model.ParamRef, error) {
	kind, err := model.ParseStripKind(p.Strip.Kind)
	if err != nil {
		return model.ParamRef{}, codeErr(CodeInvalidCommand, err)
	}
	field, err := model.ParseField(p.Field)
	if err != nil {
		return model.ParamRef{}, codeErr(CodeInvalidCommand, err)
	}
	ref := model.StripRef{Kind: kind, Index: p.Strip.Index}
	if (kind == model.StripInput && p.Strip.Index < 1) || (kind == model.StripMain && p.Strip.Index != 0) {
		return model.ParamRef{}, codeErr(CodeInvalidCommand, fmt.Errorf("strip %s index %d", kind, p.Strip.Index))
	}
	return model.ParamRef{Strip: ref, Field: field}, nil
}

func parseOrigin(o originJSON) (driver.Origin, error) {
	kind, ok := driver.ParseOriginKind(o.Kind)
	if !ok || len(o.ClientID) > maxClientIDLen {
		return driver.Origin{}, codeErr(CodeInvalidCommand, fmt.Errorf("origin %q", o.Kind))
	}
	return driver.Origin{Kind: kind, ClientID: o.ClientID, Seq: o.Seq}, nil
}

func parseValue(f model.Field, raw json.RawMessage) (model.Value, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		// json.Unmarshal(null) leaves the target unchanged: false would unmute, "" would wipe a name.
		return model.Value{}, codeErr(CodeInvalidCommand, errors.New("set needs a non-null value"))
	}
	var (
		v   model.Value
		err error
	)
	switch f {
	case model.FieldFader:
		v, err = parseLevel(raw)
	case model.FieldMute:
		var b bool
		if err = json.Unmarshal(raw, &b); err == nil {
			v = model.Bool(b)
		}
	default:
		var s string
		if err = json.Unmarshal(raw, &s); err == nil {
			v, err = model.Text(s)
		}
	}
	if err != nil {
		return model.Value{}, codeErr(CodeInvalidCommand, fmt.Errorf("%s value: %w", f, err))
	}
	return v, nil
}

func parseLevel(raw json.RawMessage) (model.Value, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s != minusInf {
			return model.Value{}, fmt.Errorf("level string %q, only %q is allowed", s, minusInf)
		}
		return model.Level(math.Inf(-1))
	}
	var db float64
	if err := json.Unmarshal(raw, &db); err != nil {
		return model.Value{}, err
	}
	return model.Level(db)
}

// levelJSON renders a level for JSON, which has no infinity.
func levelJSON(db float64) any {
	if math.IsInf(db, -1) {
		return minusInf
	}
	return db
}

func valueJSON(v model.Value) any {
	if db, ok := v.DB(); ok {
		return levelJSON(db)
	}
	if b, ok := v.Bool(); ok {
		return b
	}
	s, _ := v.Text()
	return s
}

type consoleJSON struct {
	Address  string `json:"address,omitempty"`
	Name     string `json:"name,omitempty"`
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`
}

type stripStateJSON struct {
	Kind    string `json:"kind"`
	Index   int    `json:"index,omitempty"`
	Name    string `json:"name"`
	FaderDB any    `json:"faderDB"`
	Mute    bool   `json:"mute"`
}

type changeJSON struct {
	Param  paramJSON  `json:"param"`
	Value  any        `json:"value"`
	Origin originJSON `json:"origin"`
}

type eventJSON struct {
	Type     string      `json:"type"`
	Param    paramJSON   `json:"param"`
	ClientID string      `json:"clientId"`
	Value    any         `json:"value,omitempty"`
	Origin   *originJSON `json:"origin,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

type stateJSON struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	// v0.1 connection fields, in every message.
	StaleSinceMs int64            `json:"staleSinceMs,omitempty"`
	Offline      bool             `json:"offline,omitempty"`
	Console      *consoleJSON     `json:"console,omitempty"`
	Strips       []stripStateJSON `json:"strips,omitempty"`
	Changes      *[]changeJSON    `json:"changes,omitempty"` // present (maybe empty) in every diff
	Events       []eventJSON      `json:"events,omitempty"`
}

// encodeUpdate renders a supervisor update as a state message with sequence seq.
func encodeUpdate(u console.Update, seq uint64, full bool) ([]byte, error) {
	out := stateJSON{Version: contractVersion, Seq: seq, Status: u.Status.String(), Reason: u.Reason, Events: eventsJSON(u.Events),
		Offline: u.Offline}
	if !u.StaleSince.IsZero() {
		out.StaleSinceMs = u.StaleSince.UnixMilli()
	}
	if !full {
		out.Type = "diff"
		changes := make([]changeJSON, 0, len(u.Changes))
		for _, c := range u.Changes {
			changes = append(changes, changeJSON{Param: paramToJSON(c.Param), Value: valueJSON(c.Value), Origin: originToJSON(c.Origin)})
		}
		out.Changes = &changes
		return json.Marshal(out)
	}
	out.Type = "snapshot"
	id := u.Identity
	out.Console = &consoleJSON{Address: id.Address, Name: id.Name, Model: id.Model, Firmware: id.Firmware}
	out.Strips = make([]stripStateJSON, 0, len(u.State.Inputs())+1)
	for _, st := range u.State.Strips() {
		out.Strips = append(out.Strips, stripStateJSON{
			Kind: st.Ref.Kind.String(), Index: st.Ref.Index, Name: st.Name, FaderDB: levelJSON(st.FaderDB), Mute: st.Muted,
		})
	}
	return json.Marshal(out)
}

func eventsJSON(evs []console.HoldEvent) []eventJSON {
	out := make([]eventJSON, 0, len(evs))
	for _, ev := range evs {
		e := eventJSON{Type: ev.Kind.String(), Param: paramToJSON(ev.Param), ClientID: ev.ClientID, Reason: ev.Reason}
		if ev.Kind == console.HoldExternalChange {
			o := originToJSON(ev.Origin)
			e.Value, e.Origin = valueJSON(ev.Value), &o
		}
		out = append(out, e)
	}
	return out
}

func paramToJSON(p model.ParamRef) paramJSON {
	return paramJSON{Strip: stripJSON{Kind: p.Strip.Kind.String(), Index: p.Strip.Index}, Field: p.Field.String()}
}

func originToJSON(o driver.Origin) originJSON {
	return originJSON{Kind: o.Kind.String(), ClientID: o.ClientID, Seq: o.Seq}
}
