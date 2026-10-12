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
	cmdToggle       = "toggle"
	cmdOpenWall     = "openWall"     // v0.3
	cmdUnlock       = "unlock"       // v0.3
	cmdRelock       = "relock"       // v0.3
	cmdSlipGuard    = "setSlipGuard" // v0.3
	cmdShowMode     = "setShowMode"  // v0.3
	ceilingOff      = "off"
	jsonNull        = "null"
	maxHeadroomDB   = 10.0
	fieldEffective  = "effectiveMute"
	maxGroupNumber  = 32 // largest group number a membership list may name
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
	Safety safetyJSON `json:"safety"`
}

// safetyJSON is config.schema.json safety (v0.3).
type safetyJSON struct {
	SlipGuard      bool            `json:"slipGuard"`
	ShowMode       bool            `json:"showMode"`
	MonitorCeiling json.RawMessage `json:"monitorCeiling,omitempty"`
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
	Version   *int            `json:"version"`
	Type      string          `json:"type"`
	Param     *paramJSON      `json:"param"`
	Value     json.RawMessage `json:"value,omitempty"`
	Origin    originJSON      `json:"origin"`
	Typed     *bool           `json:"typed"`     // v0.3, set only
	Confirmed *bool           `json:"confirmed"` // v0.3, set only
	Undo      *bool           `json:"undo"`      // v0.3, set only
}

// command is a parsed, validated Apply input.
type command struct {
	kind                   string
	param                  model.ParamRef
	value                  model.Value
	origin                 driver.Origin
	typed, confirmed, undo bool // set only (v0.3)
	on                     bool // setSlipGuard, setShowMode
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
	if !knownFamily(c.Family) {
		return c, codeErr(CodeInvalidConfig, fmt.Errorf("unknown family %q", c.Family))
	}
	if _, err := netip.ParseAddr(c.Host); err != nil {
		return c, codeErr(CodeInvalidConfig, fmt.Errorf("host must be an IP address: %w", err))
	}
	if c.Port < 0 || c.Port > math.MaxUint16 {
		return c, codeErr(CodeInvalidConfig, fmt.Errorf("port %d", c.Port))
	}
	if _, err := c.Safety.monitor(); err != nil {
		return c, codeErr(CodeInvalidConfig, err)
	}
	return c, nil
}

// monitor parses monitorCeiling: absent or "off" -> nil; {headroomDB} with
// a default of 6 dB, 0..10 (SR-28). null is refused: it would otherwise
// decode as {} and switch the ceiling on.
func (s safetyJSON) monitor() (*console.MonitorCeiling, error) {
	raw := bytes.TrimSpace(s.MonitorCeiling)
	if len(raw) == 0 || string(raw) == `"`+ceilingOff+`"` {
		return nil, nil
	}
	if string(raw) == jsonNull {
		return nil, errors.New(`monitorCeiling: null; use "off" or {headroomDB}`)
	}
	var m struct {
		HeadroomDB *float64 `json:"headroomDB"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("monitorCeiling: %w", err)
	}
	h := 6.0 // SR-28 default headroom
	if m.HeadroomDB != nil {
		h = *m.HeadroomDB
	}
	if math.IsNaN(h) || h < 0 || h > maxHeadroomDB {
		return nil, fmt.Errorf("monitorCeiling headroomDB %v outside 0..%v", h, maxHeadroomDB)
	}
	return &console.MonitorCeiling{HeadroomDB: h}, nil
}

func parseCommand(data string) (command, error) {
	var c commandJSON
	if err := decodeStrict(data, &c); err != nil {
		return command{}, codeErr(CodeInvalidCommand, err)
	}
	if c.Version == nil || *c.Version != contractVersion {
		return command{}, codeErr(CodeInvalidCommand, errVersion)
	}
	o, err := parseOrigin(c.Origin)
	if err != nil {
		return command{}, err
	}
	cmd := command{kind: c.Type, origin: o}
	if (c.Typed != nil || c.Confirmed != nil || c.Undo != nil) && c.Type != cmdSet {
		return command{}, codeErr(CodeInvalidCommand, errors.New("typed, confirmed and undo go with set only"))
	}
	switch c.Type {
	case cmdSlipGuard, cmdShowMode:
		return parseSetting(c, cmd)
	case cmdSet, cmdHoldBegin, cmdHoldEnd, cmdToggle, cmdOpenWall, cmdUnlock, cmdRelock:
	default:
		return command{}, codeErr(CodeInvalidCommand, fmt.Errorf("type %q", c.Type))
	}
	if c.Param == nil {
		return command{}, codeErr(CodeInvalidCommand, errors.New("param is required"))
	}
	if cmd.param, err = parseParam(*c.Param); err != nil {
		return command{}, err
	}
	if c.Type != cmdSet {
		if len(c.Value) != 0 {
			return command{}, codeErr(CodeInvalidCommand, errors.New("only set and settings carry a value"))
		}
		return cmd, nil
	}
	cmd.typed, cmd.confirmed, cmd.undo = isTrue(c.Typed), isTrue(c.Confirmed), isTrue(c.Undo)
	if (cmd.typed || cmd.confirmed) && o.Kind != driver.OriginGesture {
		return command{}, codeErr(CodeInvalidCommand, errors.New("typed and confirmed need origin gesture (SR-31 bounds macros)"))
	}
	if cmd.value, err = parseValue(cmd.param.Field, c.Value); err != nil {
		return command{}, err
	}
	return cmd, nil
}

func isTrue(b *bool) bool { return b != nil && *b }

// parseSetting parses setSlipGuard / setShowMode: a boolean value, no param.
func parseSetting(c commandJSON, cmd command) (command, error) {
	if c.Param != nil {
		return command{}, codeErr(CodeInvalidCommand, errors.New("settings carry no param"))
	}
	if err := json.Unmarshal(c.Value, &cmd.on); err != nil || len(c.Value) == 0 {
		return command{}, codeErr(CodeInvalidCommand, fmt.Errorf("%s needs a boolean value", c.Type))
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
	if (kind != model.StripMain && p.Strip.Index < 1) || (kind == model.StripMain && p.Strip.Index != 0) {
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
	case model.FieldFader, model.FieldGain:
		v, err = parseLevel(raw)
	case model.FieldMute:
		var b bool
		if err = json.Unmarshal(raw, &b); err == nil {
			v = model.Bool(b)
		}
	case model.FieldMuteGroups, model.FieldDCAs:
		var list []int
		if err = json.Unmarshal(raw, &list); err == nil {
			var m uint32
			m, err = model.MaskOf(list, maxGroupNumber)
			v = model.Mask(m)
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
	if m, ok := v.Mask(); ok {
		return model.MaskMembers(m)
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
	// v0.1: membership (absent when the console has no such groups) and the
	// derived effective mute.
	MuteGroups    *[]int             `json:"muteGroups,omitempty"`
	DCAs          *[]int             `json:"dcas,omitempty"`
	EffectiveMute *effectiveMuteJSON `json:"effectiveMute,omitempty"`
}

type changeJSON struct {
	Param  paramJSON  `json:"param"`
	Value  any        `json:"value"`
	Origin originJSON `json:"origin"`
}

// eventJSON is one entry of events: a hold event (param, clientId, ...) or,
// v0.1, a scene recall (confidence, signals, highlightedScene; v0.2 loadedScene).
type eventJSON struct {
	Type             string      `json:"type"`
	Param            *paramJSON  `json:"param,omitempty"`
	ClientID         *string     `json:"clientId,omitempty"`
	Value            any         `json:"value,omitempty"`
	Origin           *originJSON `json:"origin,omitempty"`
	Reason           string      `json:"reason,omitempty"`
	Confidence       string      `json:"confidence,omitempty"`
	Signals          []string    `json:"signals,omitempty"`
	HighlightedScene *int        `json:"highlightedScene,omitempty"`
	Index            *int        `json:"index,omitempty"`       // v0.3 gainUnlockEnded
	LoadedScene      *int        `json:"loadedScene,omitempty"` // v0.2
}

type stateJSON struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	// v0.1 connection fields, in every message.
	StaleSinceMs  int64             `json:"staleSinceMs,omitempty"`
	Offline       bool              `json:"offline,omitempty"`
	Limited       bool              `json:"limited,omitempty"`
	LimitedReason string            `json:"limitedReason,omitempty"`
	ShowMode      bool              `json:"showMode,omitempty"`         // v0.3
	Unlocked      []int             `json:"unlockedHeadamps,omitempty"` // v0.3
	Console       *consoleJSON      `json:"console,omitempty"`
	Strips        []stripStateJSON  `json:"strips,omitempty"`
	AbsentInputs  []int             `json:"absentInputs,omitempty"` // v0.2, snapshot only
	Headamps      []headampJSON     `json:"headamps,omitempty"`     // v0.3, snapshot only
	HeadampRange  *headampRangeJSON `json:"headampRange,omitempty"` // v0.3, snapshot only
	MuteGroups    []groupStateJSON  `json:"muteGroups,omitempty"`   // v0.1, snapshot only
	DCAs          []groupStateJSON  `json:"dcas,omitempty"`         // v0.1, snapshot only
	Changes       *[]changeJSON     `json:"changes,omitempty"`      // present (maybe empty) in every diff
	Events        []eventJSON       `json:"events,omitempty"`
}

// encodeUpdate renders a supervisor update as a state message with sequence seq.
func encodeUpdate(u console.Update, seq uint64, full bool) ([]byte, error) {
	out := stateJSON{Version: contractVersion, Seq: seq, Status: u.Status.String(), Reason: u.Reason,
		Events:  append(append(eventsJSON(u.Events), sceneEventsJSON(u.Scenes)...), unlockEventsJSON(u.UnlockEnded)...),
		Offline: u.Offline, Limited: u.Limited, LimitedReason: u.LimitedReason, ShowMode: u.ShowMode}
	if u.ShowMode {
		out.Unlocked = u.Unlocked
	}
	if !u.StaleSince.IsZero() {
		out.StaleSinceMs = u.StaleSince.UnixMilli()
	}
	if !full {
		out.Type = "diff"
		changes := make([]changeJSON, 0, len(u.Changes)+len(u.Effective))
		for _, c := range u.Changes {
			changes = append(changes, changeJSON{Param: paramToJSON(c.Param), Value: valueJSON(c.Value), Origin: originToJSON(c.Origin)})
		}
		changes = append(changes, effectiveChangesJSON(u.Effective)...)
		out.Changes = &changes
		return json.Marshal(out)
	}
	out.Type = "snapshot"
	id := u.Identity
	out.Console = &consoleJSON{Address: id.Address, Name: id.Name, Model: id.Model, Firmware: id.Firmware}
	out.Strips = stripsJSON(u.State, u.Capabilities)
	out.AbsentInputs = absentInputsJSON(u.Capabilities)
	out.Headamps, out.HeadampRange = headampsJSON(u.State, u.Capabilities)
	out.MuteGroups, out.DCAs = groupStatesJSON(u.State.MuteGroups()), groupStatesJSON(u.State.DCAs())
	return json.Marshal(out)
}

func eventsJSON(evs []console.HoldEvent) []eventJSON {
	out := make([]eventJSON, 0, len(evs))
	for _, ev := range evs {
		p, client := paramToJSON(ev.Param), ev.ClientID
		e := eventJSON{Type: ev.Kind.String(), Param: &p, ClientID: &client, Reason: ev.Reason}
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
