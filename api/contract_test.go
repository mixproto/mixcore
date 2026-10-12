// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// The examples printed in the contract must be accepted by the parser.
func TestContractExamples_Parse(t *testing.T) {
	for _, tc := range []struct {
		file  string
		parse func(string) error
	}{
		{"../contracts/core-api/command.schema.json", func(s string) error { _, err := parseCommand(s); return err }},
		{"../contracts/core-api/config.schema.json", func(s string) error { _, err := parseConfig(s); return err }},
		{"../contracts/core-api/discover.schema.json", func(s string) error { _, err := parseDiscover(s); return err }},
		{"../contracts/core-api/discovery.schema.json", func(s string) error {
			var m discoveryResultJSON
			return decodeStrict(s, &m)
		}},
	} {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Examples []json.RawMessage `json:"examples"`
		}
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		if len(schema.Examples) == 0 {
			t.Fatalf("%s has no examples", tc.file)
		}
		for _, ex := range schema.Examples {
			if err := tc.parse(string(ex)); err != nil {
				t.Fatalf("%s example %s rejected: %v", tc.file, ex, err)
			}
		}
	}
}

// stateExamples renders one snapshot and one diff; used by the test below and
// written to testdata so the JSON Schema can be checked with an external validator.
func stateExamples(t *testing.T) [][]byte {
	t.Helper()
	main := model.Strip{Ref: model.MainRef, FaderDB: math.Inf(-1)}
	kick := model.StripRef{Kind: model.StripInput, Index: 1}
	st, err := model.NewState([]model.Strip{{Ref: kick, Name: "Kick", FaderDB: -3.5, MuteGroups: 0b10, DCAs: 0b1}}, &main)
	if err != nil {
		t.Fatal(err)
	}
	st = st.WithGroups(2, 1)
	mg2 := model.ParamRef{Strip: model.StripRef{Kind: model.StripMuteGroup, Index: 2}, Field: model.FieldMute}
	if st, err = st.With(mg2, model.Bool(true)); err != nil {
		t.Fatal(err)
	}
	lv, _ := model.Level(-6)
	desk := driver.Origin{Kind: driver.OriginConsole}
	u := console.Update{Status: console.StatusLive, State: st, Identity: driver.Identity{Name: "FOH", Model: "X32"},
		Changes: []console.Change{{Param: model.ParamRef{Strip: model.MainRef, Field: model.FieldFader}, Value: lv,
			Origin: driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 3}}, {Param: mg2, Value: model.Bool(true), Origin: desk}},
		Effective: []console.EffectiveChange{{Strip: kick, Mute: st.EffectiveMute(kick), Origin: desk}},
		Events: []console.HoldEvent{
			{Kind: console.HoldExternalChange, Param: model.ParamRef{Strip: model.MainRef, Field: model.FieldFader}, ClientID: "ios-1",
				Value: lv, Origin: driver.Origin{Kind: driver.OriginConsole}},
			{Kind: console.HoldCancelled, Param: model.ParamRef{Strip: model.MainRef, Field: model.FieldFader}, ClientID: "ios-2", Reason: "stale"},
		}}
	snap, err := encodeUpdate(u, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := encodeUpdate(u, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{snap, diff}
}

func TestStateJSON_Golden(t *testing.T) {
	ex := stateExamples(t)
	wantSnap := `{"version":0,"type":"snapshot","seq":1,"status":"live","console":{"name":"FOH","model":"X32"},` +
		`"strips":[{"kind":"input","index":1,"name":"Kick","faderDB":-3.5,"mute":false,"muteGroups":[2],"dcas":[1],` +
		`"effectiveMute":{"muted":true,"own":false,"muteGroups":[2],"dcas":[]}},` +
		`{"kind":"main","name":"","faderDB":"-inf","mute":false,"muteGroups":[],"effectiveMute":{"muted":false,"own":false,"muteGroups":[],"dcas":[]}}],` +
		`"muteGroups":[{"index":1,"mute":false},{"index":2,"mute":true}],"dcas":[{"index":1,"mute":false}],` +
		`"events":[{"type":"externalChangeOnHeld","param":{"strip":{"kind":"main"},"field":"fader"},"clientId":"ios-1","value":-6,"origin":{"kind":"console"}},` +
		`{"type":"holdCancelled","param":{"strip":{"kind":"main"},"field":"fader"},"clientId":"ios-2","reason":"stale"}]}`
	wantDiff := `{"version":0,"type":"diff","seq":2,"status":"live","changes":[{"param":{"strip":{"kind":"main"},"field":"fader"},` +
		`"value":-6,"origin":{"kind":"gesture","clientId":"ui","seq":3}},` +
		`{"param":{"strip":{"kind":"muteGroup","index":2},"field":"mute"},"value":true,"origin":{"kind":"console"}},` +
		`{"param":{"strip":{"kind":"input","index":1},"field":"effectiveMute"},"value":{"muted":true,"own":false,"muteGroups":[2],"dcas":[]},"origin":{"kind":"console"}}],` +
		`"events":[{"type":"externalChangeOnHeld","param":{"strip":{"kind":"main"},"field":"fader"},"clientId":"ios-1","value":-6,"origin":{"kind":"console"}},` +
		`{"type":"holdCancelled","param":{"strip":{"kind":"main"},"field":"fader"},"clientId":"ios-2","reason":"stale"}]}`
	if string(ex[0]) != wantSnap {
		t.Fatalf("snapshot\n got %s\nwant %s", ex[0], wantSnap)
	}
	if string(ex[1]) != wantDiff {
		t.Fatalf("diff\n got %s\nwant %s", ex[1], wantDiff)
	}
	if os.Getenv("MIXCORE_WRITE_STATE_EXAMPLES") != "" {
		for i, name := range []string{"testdata/state-snapshot.json", "testdata/state-diff.json"} {
			if err := os.WriteFile(name, ex[i], 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func FuzzParseCommand(f *testing.F) {
	f.Add(cmd("fader", "-3.5", "gesture"))
	f.Add(cmd("fader", `"-inf"`, "macro"))
	f.Add(cmd("mute", "true", "console"))
	f.Add(`{"version":0,"type":"holdBegin","param":{"strip":{"kind":"main"},"field":"fader"},"origin":{"kind":"gesture"}}`)
	f.Add(`{"version":0,"type":"toggle","param":{"strip":{"kind":"muteGroup","index":2},"field":"mute"},"origin":{"kind":"gesture"}}`)
	f.Add(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":3},"field":"muteGroups"},"value":[1,6],"origin":{"kind":"gesture"}}`)
	f.Fuzz(func(t *testing.T, in string) {
		c, err := parseCommand(in)
		if err != nil {
			if CodeOf(err) != CodeInvalidCommand && CodeOf(err) != CodeInvalidValue {
				t.Fatalf("parse error with code %s", CodeOf(err))
			}
			return
		}
		if c.kind == cmdSet {
			if db, ok := c.value.DB(); ok && (math.IsNaN(db) || math.IsInf(db, 1)) {
				t.Fatalf("parser accepted level %v", db)
			}
			if c.value.Kind() != c.param.Field.ValueKind() {
				t.Fatalf("value kind %d for field %s", c.value.Kind(), c.param.Field)
			}
		}
	})
}
