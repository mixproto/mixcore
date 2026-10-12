// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// faderScene moves the faders of channels 1..n to 0.5 (-10 dB).
func faderScene(n int) map[string]osc.Arg {
	out := map[string]osc.Arg{}
	for ch := 1; ch <= n; ch++ {
		out[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.5)
	}
	return out
}

// X32-F60: a recall is a burst of pushes; the markers are an [UNVERIFIED]
// variant. Confidence follows the signals; a re-read starts at once.
func TestSceneRecall_DetectedWithConfidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		markers bool
		want    string
		signals []string
	}{
		{"burst only (documented)", false, driver.ConfidenceLow, []string{driver.SignalBurst}},
		{"burst and markers", true, driver.ConfidenceHigh, []string{driver.SignalBurst, driver.SignalUndoTime, driver.SignalHighlightedScene}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, simx32.Options{PushSceneMarkers: tc.markers})
				sess := r.connect()
				events, _ := r.run(sess)
				before := len(reads(r.sim, "/ch/32/config/name"))
				if err := r.sim.RecallScene(12, faderScene(16)); err != nil {
					t.Fatal(err)
				}
				ev, ok := nextOf[driver.SceneRecalled](events, time.Second)
				if !ok || ev.Confidence != tc.want || !slices.Equal(ev.Signals, tc.signals) {
					t.Fatalf("scene event = %+v, %v; want %s %v", ev, ok, tc.want, tc.signals)
				}
				if tc.markers && ev.HighlightedScene != 12 {
					t.Fatalf("highlighted scene = %d, want 12", ev.HighlightedScene)
				}
				time.Sleep(rereadPassTime)
				if len(reads(r.sim, "/ch/32/config/name")) == before {
					t.Fatal("a scene recall must start a background re-read")
				}
				if len(r.sim.Writes()) != 0 {
					t.Fatal("detection must never write")
				}
			})
		})
	}
}

// A marker without a burst is medium; a desk move or our own writes are no scene.
func TestSceneRecall_MarkerAloneAndNonScenes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		_ = r.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.5)) // one engineer, one fader
		for ch := 1; ch <= 10; ch++ {                            // ten writes of ours: echoes, not the console
			mute(t, sess, ch, true)
		}
		if ev, ok := nextOf[driver.SceneRecalled](events, time.Second); ok {
			t.Fatalf("no scene expected, got %+v", ev)
		}
		_ = r.sim.SurfaceSet("/-undo/time", osc.String("12:00:01"))
		ev, ok := nextOf[driver.SceneRecalled](events, time.Second)
		if !ok || ev.Confidence != driver.ConfidenceMedium || ev.HighlightedScene != driver.NoScene {
			t.Fatalf("marker alone = %+v, %v", ev, ok)
		}
	})
}
