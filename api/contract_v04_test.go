// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// v04State is a small x32-like console of core-api v0.4: one input, main,
// a DCA with its master, buses 1-2 (a linked pair) and FX bus 7, one
// matrix, colours, sends with on, pair-scoped pan and tap.
func v04State(t *testing.T) model.State {
	t.Helper()
	main := model.Strip{Ref: model.MainRef, FaderDB: math.Inf(-1)}
	in1 := model.StripRef{Kind: model.StripInput, Index: 1}
	st, err := model.NewState([]model.Strip{{Ref: in1, Name: "Vox1", FaderDB: -6}}, &main)
	if err != nil {
		t.Fatal(err)
	}
	if st, err = st.WithGroups(0, 1).WithBuses([]model.BusInfo{{Index: 1, Kind: model.BusMix, Linkable: true, SendPan: true},
		{Index: 2, Kind: model.BusMix, Linkable: true, SendPan: true}, {Index: 7, Kind: model.BusFX}}); err != nil {
		t.Fatal(err)
	}
	st = st.WithMatrices(1).WithShape(model.Shape{Colours: true, DCAMasters: true, BusLinks: true,
		Sends: model.SendShape{Level: true, On: true, Pan: true, Tap: true, PairScoped: true}})
	bus := func(b int, f model.Field) model.ParamRef {
		return model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: b}, Field: f}
	}
	send := func(b int, f model.Field) model.ParamRef { return model.ParamRef{Strip: in1, Field: f, Bus: b} }
	blue, _ := model.ColourValue(4)
	red, _ := model.ColourValue(1)
	pan, _ := model.Pan(-40)
	pre, _ := model.TapValue(model.TapPreFader)
	name, _ := model.Text("Mon 2 Keys")
	delays, _ := model.Text("Delays")
	band, _ := model.Text("Band")
	lv := func(db float64) model.Value { v, _ := model.Level(db); return v }
	for _, set := range []struct {
		p model.ParamRef
		v model.Value
	}{
		{bus(2, model.FieldName), name}, {bus(2, model.FieldColour), blue}, {bus(2, model.FieldFader), lv(-3)}, {bus(1, model.FieldLinked), model.Bool(true)},
		{send(2, model.FieldSendLevel), lv(-8)}, {send(2, model.FieldSendOn), model.Bool(true)}, {send(1, model.FieldSendPan), pan},
		{send(1, model.FieldSendTap), pre}, {model.ParamRef{Strip: in1, Field: model.FieldColour}, red},
		{model.ParamRef{Strip: model.StripRef{Kind: model.StripMatrix, Index: 1}, Field: model.FieldName}, delays},
		{model.ParamRef{Strip: model.StripRef{Kind: model.StripDCA, Index: 1}, Field: model.FieldName}, band},
		{model.ParamRef{Strip: model.StripRef{Kind: model.StripDCA, Index: 1}, Field: model.FieldFader}, lv(-5)},
	} {
		if st, err = st.With(set.p, set.v); err != nil {
			t.Fatalf("%s: %v", set.p, err)
		}
	}
	return st
}

func v04Examples(t *testing.T) (snapshot, diff []byte) {
	t.Helper()
	st := v04State(t)
	in1 := model.StripRef{Kind: model.StripInput, Index: 1}
	lv, _ := model.Level(-6)
	green, _ := model.ColourValue(2)
	u := console.Update{Status: console.StatusLive, State: st, Identity: driver.Identity{Name: "FOH", Model: "X32"},
		Capabilities: model.Capabilities{InputCount: 1, HasMain: true, DCAs: 1, TapLabels: [model.TapCount]string{"IN/LC", "<-EQ", "EQ->", "PRE", "POST", "GRP"}},
		Changes: []console.Change{
			{Param: model.ParamRef{Strip: in1, Field: model.FieldSendLevel, Bus: 2}, Value: lv, Origin: driver.Origin{Kind: driver.OriginGesture, ClientID: "mon", Seq: 4}},
			{Param: model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: 2}, Field: model.FieldColour}, Value: green, Origin: driver.Origin{Kind: driver.OriginConsole}},
			{Param: model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: 1}, Field: model.FieldLinked}, Value: model.Bool(false), Origin: driver.Origin{Kind: driver.OriginConsole}},
		}}
	snap, err := encodeUpdate(u, 1, true, false)
	if err != nil {
		t.Fatal(err)
	}
	d, err := encodeUpdate(u, 2, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return snap, d
}

// Review Go S4: the v0.4 snapshot and diff are pinned byte for byte and
// valid against state.schema.json.
func TestStateJSON_GoldenV04(t *testing.T) {
	snap, diff := v04Examples(t)
	// encoding/json escapes < and > in the console's tap words (<-EQ, EQ->).
	want := strings.NewReplacer("<", `\`+"u003c", ">", `\`+"u003e").Replace(goldenSnapV04)
	if string(snap) != want {
		t.Fatalf("snapshot\n got %s\nwant %s", snap, want)
	}
	if string(diff) != goldenDiffV04 {
		t.Fatalf("diff\n got %s\nwant %s", diff, goldenDiffV04)
	}
	if os.Getenv("MIXCORE_WRITE_STATE_EXAMPLES") != "" {
		for name, b := range map[string][]byte{"testdata/state-snapshot-v04.json": snap, "testdata/state-diff-v04.json": diff} {
			if err := os.WriteFile(name, b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	schemas := loadSchemas(t)
	for _, m := range [][]byte{snap, diff} {
		if errs := schemas.validate("state.schema.json", string(m)); len(errs) != 0 {
			t.Fatalf("%s\nviolates state.schema.json: %v", m, errs)
		}
	}
}

const (
	goldenSnapV04 = `{"version":0,"type":"snapshot","seq":1,"status":"live","console":{"name":"FOH","model":"X32"},"strips":[{"kind":"input","index":1,"name":"Vox1","faderDB":-6,"mute":false,"dcas":[],"effectiveMute":{"muted":false,"basis":"derived","own":false,"muteGroups":[],"dcas":[]},"colour":"red","sends":[{"bus":1,"levelDB":"-inf","on":false,"pan":-40,"tap":"preFader"},{"bus":2,"levelDB":-8,"on":true},{"bus":7,"levelDB":"-inf","on":false,"tap":"input"}]},{"kind":"main","name":"","faderDB":"-inf","mute":false,"effectiveMute":{"muted":false,"basis":"derived","own":false,"muteGroups":[],"dcas":[]},"colour":"off"}],"dcas":[{"index":1,"mute":false,"name":"Band","colour":"off","faderDB":-5}],"buses":[{"index":1,"kind":"mix","name":"","colour":"off","faderDB":"-inf","mute":false,"linked":true,"sendPan":true},{"index":2,"kind":"mix","name":"Mon 2 Keys","colour":"blue","faderDB":-3,"mute":false,"sendPan":true},{"index":7,"kind":"fx","name":"","colour":"off","faderDB":"-inf","mute":false}],"matrices":[{"index":1,"name":"Delays","colour":"off","faderDB":"-inf","mute":false}],"sendFields":{"on":true,"pan":"pair","tap":"pair"},"sendTaps":[{"tap":"input","label":"IN/LC"},{"tap":"preEq","label":"<-EQ"},{"tap":"postEq","label":"EQ->"},{"tap":"preFader","label":"PRE"},{"tap":"postFader","label":"POST"},{"tap":"group","label":"GRP"}]}`
	goldenDiffV04 = `{"version":0,"type":"diff","seq":2,"status":"live","changes":[{"param":{"strip":{"kind":"input","index":1},"field":"sendLevel","bus":2},"value":-6,"origin":{"kind":"gesture","clientId":"mon","seq":4}},{"param":{"strip":{"kind":"bus","index":2},"field":"colour"},"value":"green","origin":{"kind":"console"}},{"param":{"strip":{"kind":"bus","index":1},"field":"linked"},"value":false,"origin":{"kind":"console"}}]}`
)
