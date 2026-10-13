// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"strings"
	"testing"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Every v0.5 field of state.schema.json in one snapshot and one diff,
// pinned in part and valid against the schema.
func TestStateJSON_V05FieldsMatchTheSchema(t *testing.T) {
	st := v04State(t)
	in1 := model.StripRef{Kind: model.StripInput, Index: 1}
	lv := func(db float64) model.Value { v, _ := model.Level(db); return v }
	u := console.Update{Status: console.StatusLive, State: st, ShowMode: true, ShowModeLocked: true,
		Capabilities: model.Capabilities{InputCount: 1, HasMain: true, DCAs: 1},
		Favourites:   []model.StripRef{{Kind: model.StripBus, Index: 2}, in1, model.MainRef},
		Undo: []console.UndoSummary{
			{ClientID: "ui", Param: model.ParamRef{Strip: in1, Field: model.FieldFader}, Kind: console.UndoLevel, From: lv(-6), To: lv(-2),
				Delta: 4, HasDelta: true, Direction: console.DirectionUp, Asks: []string{console.AskBeforeSceneRecall},
				Scene: &console.SceneMark{Number: 4, Name: "Song 4"}},
			{ClientID: "mon", Param: model.ParamRef{Strip: in1, Field: model.FieldSendOn, Bus: 2}, Kind: console.UndoSwitch,
				From: model.Bool(true), To: model.Bool(false), Direction: console.DirectionNone, Asks: []string{console.AskUnmute}},
		},
		Scopes: []console.ScopeEvent{{ClientID: "ui", Bus: 2, Count: 3}},
		Scenes: []driver.SceneRecalled{{Confidence: driver.ConfidenceLow, Signals: []string{driver.SignalBurst}, HighlightedScene: driver.NoScene,
			LoadedScene: 4, SceneName: "Song 4"}},
	}
	snap, err := encodeUpdate(u, 1, true, false)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := encodeUpdateWith(u, 2, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"showModeLocked":true,"favourites":[{"kind":"bus","index":2},{"kind":"input","index":1},{"kind":"main"}],"undo":[{"clientId":"ui",` +
			`"param":{"strip":{"kind":"input","index":1},"field":"fader"},"kind":"level","from":-6,"to":-2,"delta":4,"direction":"up",` +
			`"asks":["beforeSceneRecall"],"scene":{"number":4,"name":"Song 4"}},{"clientId":"mon",` +
			`"param":{"strip":{"kind":"input","index":1},"field":"sendOn","bus":2},"kind":"switch","from":true,"to":false,"direction":"none","asks":["unmute"]}]`,
		`{"type":"sceneRecalledAtConsole","confidence":"low","signals":["burst"],"loadedScene":4,"sceneNumber":4,"sceneName":"Song 4"}`,
		`{"type":"externalChangeInScope","clientId":"ui","scope":{"bus":2},"count":3}`,
	} {
		if !strings.Contains(string(snap), want) || !strings.Contains(string(diff), want) {
			t.Fatalf("missing %s\nsnapshot %s\ndiff %s", want, snap, diff)
		}
	}
	schemas := loadSchemas(t)
	for _, m := range [][]byte{snap, diff} {
		if errs := schemas.validate("state.schema.json", string(m)); len(errs) != 0 {
			t.Fatalf("%s\nviolates state.schema.json: %v", m, errs)
		}
	}
}
