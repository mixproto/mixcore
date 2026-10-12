// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Phone safety (contracts/core-api README v0.3) through the API, against
// the X Air simulator. Fixture of acceptance/phone-safety.md: Vox1 (ch 1)
// at -6.0 dB, headamp 3 Gain +32.5 dB.

func startWith(t *testing.T, e *xairEnv, safety string) {
	t.Helper()
	_ = e.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(float32((-6.0+30)/40))) // -6 dB (XA-F40)
	_ = e.sim.SurfaceSet("/headamp/03/gain", osc.Float(89.0/144))              // +32.5 dB
	cfg := fmt.Sprintf(`{"version":0,"family":"xair","host":"10.0.0.1","safety":%s}`, safety)
	if _, err := e.eng.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(3 * time.Second) // first connect probes the optional units
	synctest.Wait()
}

func cmdJSON(typ, param, extra string) string {
	p := ""
	if param != "" {
		p = `,"param":` + param
	}
	return `{"version":0,"type":"` + typ + `"` + p + extra + `,"origin":{"kind":"gesture","clientId":"ui","seq":1}}`
}

const (
	vox1  = `{"strip":{"kind":"input","index":1},"field":"fader"}`
	gain3 = `{"strip":{"kind":"headamp","index":3},"field":"gain"}`
)

// CR-22 through Gesture: the window, the wall, openWall, and the JSON.
func TestSafety_GestureResultsAtTheWall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{"slipGuard":true}`)
		// -6 dB is stored on the console's nearest step, -5.99 dB (XA-F41): the
		// wall is 6 dB above whatever start the console reports.
		var hold gestureResultJSON
		got, err := e.eng.Gesture(cmdJSON("holdBegin", vox1, ""))
		if err != nil || json.Unmarshal([]byte(got), &hold) != nil || hold.Window == nil || hold.Window.WallUpDB == nil {
			t.Fatalf("holdBegin: %s %v", got, err)
		}
		start := hold.Window.StartDB.(float64)
		if math.Abs(*hold.Window.WallUpDB-(start+6)) > 1e-9 || math.Abs(start+6) > 0.01 || !hold.Window.OpensByRest || hold.Written {
			t.Fatalf("window %s", got)
		}
		wall := fmt.Sprintf(`"state":"atWall","value":%v,"written":true`, *hold.Window.WallUpDB)
		for _, step := range []struct{ cmd, want string }{
			{cmdJSON("set", vox1, `,"value":3`), wall},
			{cmdJSON("openWall", vox1, ""), `"state":"free"`},
			{cmdJSON("holdBegin", gain3, ""), `"window":{"startDB":32.5,"capUpDB":35.5,"capDownDB":26.5,"opensByRest":false}`},
			{cmdJSON("set", gain3, `,"value":60`), `"state":"atCap","value":35.5,"written":true`},
		} {
			got, err := e.eng.Gesture(step.cmd)
			if err != nil || !strings.Contains(got, step.want) {
				t.Fatalf("%s\n got %s %v\nwant %s", step.cmd, got, err, step.want)
			}
		}
		synctest.Wait()
		if v, _ := e.sim.Value("/headamp/03/gain"); v.Float != 95.0/144 {
			t.Fatalf("mixer Gain %v, want +35.5 dB", v.Float)
		}
	})
}

// AC-PS-19, AC-PS-16 through the API: the Gain bound, the typed confirm.
func TestSafety_GainBoundAndTypedConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{}`)
		for cmd, code := range map[string]string{
			cmdJSON("set", gain3, `,"value":45`):                               CodeGainBound,
			cmdJSON("set", gain3, `,"value":45,"undo":true`):                   CodeGainBound,
			cmdJSON("set", gain3, `,"value":45,"typed":true`):                  CodeNeedsConfirm,
			cmdJSON("set", vox1, `,"value":4,"typed":true`):                    CodeNeedsConfirm,
			cmdJSON("set", gain3, `,"value":61,"typed":true,"confirmed":true`): CodeInvalidValue,
		} {
			if err := e.eng.Apply(cmd); CodeOf(err) != code {
				t.Errorf("%s -> %v, want %s", cmd, err, code)
			}
		}
		synctest.Wait()
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("%d writes from refused commands", n)
		}
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":45,"typed":true,"confirmed":true`)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if v, _ := e.sim.Value("/headamp/03/gain"); v.Float != 114.0/144 {
			t.Fatalf("mixer Gain %v, want +45 dB", v.Float)
		}
	})
}

// AC-PS-12/13/14 through the API: show mode from the config survives a
// reconnect, refuses a Gain raise, lets a lowering through, and unlocks.
func TestSafety_ShowModeThroughTheAPI(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{"showMode":true}`)
		e.net.SetDown("10.0.0.1", true)
		time.Sleep(3 * time.Second)
		e.net.SetDown("10.0.0.1", false)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if st := e.rec.last(); st.Status != "live" || !st.ShowMode {
			t.Fatalf("after reconnect: %s show %t", st.Status, st.ShowMode)
		}
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":33.5`)); CodeOf(err) != CodeShowMode {
			t.Fatalf("raise: %v", err)
		}
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":31.5`)); err != nil {
			t.Fatalf("-1: %v", err)
		}
		if err := e.eng.Apply(cmdJSON("unlock", gain3, "")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if st := e.rec.last(); len(st.Unlocked) != 1 || st.Unlocked[0] != 3 {
			t.Fatalf("unlocked %v", st.Unlocked)
		}
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":32.5`)); err != nil {
			t.Fatalf("+1 unlocked: %v", err)
		}
		_ = e.eng.Apply(cmdJSON("relock", gain3, ""))
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		e.rec.mu.Lock()
		joined := strings.Join(e.rec.raw, "\n")
		e.rec.mu.Unlock()
		if !strings.Contains(joined, `{"type":"gainUnlockEnded","reason":"relock","index":3}`) {
			t.Fatalf("no gainUnlockEnded event in\n%s", joined)
		}
		if err := e.eng.Apply(cmdJSON("setShowMode", "", `,"value":false`)); err != nil {
			t.Fatal(err)
		}
		if n := len(e.sim.Writes()); n != 2 {
			t.Fatalf("%d writes, want the two Gain steps", n)
		}
	})
}

// SR-28 through the config: a monitor seat is capped; a bad headroom is refused.
func TestSafety_MonitorCeilingConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		if _, err := e.eng.Start(`{"version":0,"family":"xair","host":"10.0.0.1","safety":{"monitorCeiling":{"headroomDB":11}}}`); CodeOf(err) != CodeInvalidConfig {
			t.Fatalf("headroom 11: %v", err)
		}
		startWith(t, e, `{"monitorCeiling":{}}`)
		got, err := e.eng.Gesture(cmdJSON("set", vox1, `,"value":10`))
		if err != nil || !strings.Contains(got, `"state":"atCeiling","value":0`) {
			t.Fatalf("monitor raise: %s %v", got, err)
		}
	})
}

// v0.3 snapshot fields: headamps and their range.
func TestSafety_SnapshotHeadamps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{MicHeadamps: 4})
		startWith(t, e, `{}`)
		_ = e.eng.RequestSnapshot()
		synctest.Wait()
		e.rec.mu.Lock()
		raw := e.rec.raw[len(e.rec.raw)-1]
		e.rec.mu.Unlock()
		want := `"headamps":[{"index":1,"gainDB":0},{"index":2,"gainDB":0},{"index":3,"gainDB":32.5},{"index":4,"gainDB":0}],"headampRange":{"minDB":-12,"maxDB":60,"stepDB":0.5}`
		if !strings.Contains(raw, want) {
			t.Fatalf("snapshot %s", raw)
		}
	})
}
