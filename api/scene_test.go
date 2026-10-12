// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const holdCh20 = `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":20},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`

// SR-15, CR-03: a recall at the desk (a burst of pushes, plus the scene
// markers when the console sends them) is reported as sceneRecalledAtConsole
// and ends a held fader the scene did not even touch.
func TestSceneRecall_EventEndsAnUntouchedHeldFader(t *testing.T) {
	for _, markers := range []bool{false, true} {
		t.Run(fmt.Sprintf("markers=%t", markers), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := newEnvWith(t, simx32.Options{Name: "FOH", PushSceneMarkers: markers})
				e.startLive(t)
				if err := e.eng.Apply(holdCh20); err != nil {
					t.Fatal(err)
				}
				scene := map[string]osc.Arg{}
				for ch := 1; ch <= 16; ch++ {
					scene[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.5)
				}
				t0 := len(e.rec.all())
				if err := e.sim.RecallScene(7, scene); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second)
				synctest.Wait()
				var scenes, ended []eventJSON
				for _, m := range e.rec.all()[t0:] {
					for _, ev := range m.Events {
						switch ev.Type {
						case "sceneRecalledAtConsole":
							scenes = append(scenes, ev)
						case "externalChangeOnHeld":
							ended = append(ended, ev)
						}
					}
				}
				want := map[bool]string{false: "low", true: "high"}[markers]
				if len(scenes) != 1 || scenes[0].Confidence != want || len(ended) != 1 || ended[0].Param.Strip.Index != 20 {
					t.Fatalf("scene events %+v, ended holds %+v; want one %s scene and ch 20 ended", scenes, ended, want)
				}
				if markers && (scenes[0].HighlightedScene == nil || *scenes[0].HighlightedScene != 7) {
					t.Fatalf("highlighted scene = %v", scenes[0].HighlightedScene)
				}
				if len(e.sim.Writes()) != 0 {
					t.Fatal("no writes")
				}
			})
		})
	}
}

func TestSceneEventJSON_Golden(t *testing.T) {
	b, err := json.Marshal(sceneEventsJSON([]driver.SceneRecalled{
		{Confidence: driver.ConfidenceHigh, Signals: []string{driver.SignalBurst, driver.SignalHighlightedScene}, HighlightedScene: 12},
		{Confidence: driver.ConfidenceLow, Signals: []string{driver.SignalBurst}, HighlightedScene: driver.NoScene},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"sceneRecalledAtConsole","confidence":"high","signals":["burst","highlightedScene"],"highlightedScene":12},` +
		`{"type":"sceneRecalledAtConsole","confidence":"low","signals":["burst"]}]`
	if string(b) != want {
		t.Fatalf("got %s\nwant %s", b, want)
	}
}

// A scene event alone (no hold ended, no change in the same drain) still
// reaches the listener.
func TestSceneRecall_EventAloneIsDelivered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnvWith(t, simx32.Options{Name: "FOH", PushSceneMarkers: true})
		e.startLive(t)
		t0 := len(e.rec.all())
		_ = e.sim.SurfaceSet("/-undo/time", osc.String("12:00:00")) // a marker alone: no parameter changes
		time.Sleep(time.Second)
		synctest.Wait()
		for _, m := range e.rec.all()[t0:] {
			for _, ev := range m.Events {
				if ev.Type == "sceneRecalledAtConsole" && ev.Confidence == "medium" {
					return
				}
			}
		}
		t.Fatal("the scene event never reached the listener")
	})
}
