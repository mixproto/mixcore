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
	"github.com/mixproto/mixcore/model"
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
			{cmdJSON("openWall", vox1, ""), `"state":"atWall"`}, // no rest yet: refused (wall.dwell)
			{"rest", ""},
			{cmdJSON("openWall", vox1, ""), `"state":"free"`},
			{cmdJSON("holdBegin", gain3, ""), `"window":{"startDB":32.5,"capUpDB":35.5,"capDownDB":26.5,"opensByRest":false}`},
			{cmdJSON("set", gain3, `,"value":60`), `"state":"atCap","value":35.5,"written":true`},
		} {
			if step.cmd == "rest" {
				time.Sleep(300 * time.Millisecond)
				continue
			}
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
		want := `"headamps":[{"index":1,"gainDB":0,"minDB":-12,"maxDB":60,"writeMaxDB":59.5,"stepDB":0.5,"writable":true,"bank":"local","number":1,"phantom":false},` +
			`{"index":2,"gainDB":0,"minDB":-12,"maxDB":60,"writeMaxDB":59.5,"stepDB":0.5,"writable":true,"bank":"local","number":2,"phantom":false},` +
			`{"index":3,"gainDB":32.5,"minDB":-12,"maxDB":60,"writeMaxDB":59.5,"stepDB":0.5,"writable":true,"bank":"local","number":3,"phantom":false},` +
			`{"index":4,"gainDB":0,"minDB":-12,"maxDB":60,"writeMaxDB":59.5,"stepDB":0.5,"writable":true,"bank":"local","number":4,"phantom":false}]`
		if !strings.Contains(raw, want) {
			t.Fatalf("snapshot %s", raw)
		}
	})
}

// Review MEDIUM 5 at the API: typed (or confirmed) from a macro is refused.
func TestSafety_TypedNeedsAGestureOrigin(t *testing.T) {
	for _, flags := range []string{`,"typed":true`, `,"typed":true,"confirmed":true`, `,"confirmed":true`} {
		cmd := `{"version":0,"type":"set","param":` + gain3 + `,"value":45` + flags + `,"origin":{"kind":"macro","clientId":"ui"}}`
		if _, err := parseCommand(cmd); CodeOf(err) != CodeInvalidCommand {
			t.Errorf("%s: %v", flags, err)
		}
	}
}

// Review LOW 6: "monitorCeiling": null is not "on with the default"; it is
// refused like any other malformed value, so a host that serialises an unset
// optional as null cannot cap a seat by accident.
func TestSafety_MonitorCeilingNullRefused(t *testing.T) {
	for _, v := range []string{`null`, ` null `} {
		_, err := parseConfig(`{"version":0,"family":"xair","host":"10.0.0.1","safety":{"monitorCeiling":` + v + `}}`)
		if CodeOf(err) != CodeInvalidConfig {
			t.Fatalf("monitorCeiling %q: %v, want ERR_INVALID_CONFIG", v, err)
		}
	}
}

// v0.3 fields phantom (bool) and headamp (index, null = none) parse and
// render with the types common.schema.json gives them; a set of either is
// refused later by the driver (read-only), never here by type.
func TestHeadampFields_JSONTypes(t *testing.T) {
	ph := `{"version":0,"type":"set","param":{"strip":{"kind":"headamp","index":1},"field":"phantom"},"value":true,"origin":{"kind":"gesture"}}`
	if c, err := parseCommand(ph); err != nil || c.value.Kind() != model.KindBool {
		t.Fatalf("phantom: %+v %v", c, err)
	}
	ha := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"headamp"},"value":3,"origin":{"kind":"gesture"}}`
	if c, err := parseCommand(ha); err != nil || c.value.Kind() != model.KindIndex {
		t.Fatalf("headamp: %+v %v", c, err)
	}
	for _, bad := range []string{`-1`, `"3"`, `null`} {
		if _, err := parseCommand(strings.Replace(ha, `"value":3`, `"value":`+bad, 1)); CodeOf(err) != CodeInvalidCommand {
			t.Errorf("headamp %s: %v", bad, err)
		}
	}
	none, _ := model.Index(0)
	five, _ := model.Index(5)
	if valueJSON(none) != nil || valueJSON(five) != 5 {
		t.Fatal("index renders as null or the number")
	}
}

// Review pass 3, MEDIUM 2: trackLengthPt goes with holdBegin only, 0 < pt
// <= 10000.
func TestHoldBegin_TrackLength(t *testing.T) {
	hb := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":1},"field":"fader"},"trackLengthPt":152,"origin":{"kind":"gesture"}}`
	if c, err := parseCommand(hb); err != nil || c.trackPt != 152 {
		t.Fatalf("holdBegin 152 pt: %+v %v", c, err)
	}
	for _, bad := range []string{
		strings.Replace(hb, `152`, `0`, 1), strings.Replace(hb, `152`, `-5`, 1), strings.Replace(hb, `152`, `10001`, 1),
		strings.Replace(hb, `"holdBegin"`, `"holdEnd"`, 1),
		`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":0,"trackLengthPt":152,"origin":{"kind":"gesture"}}`,
	} {
		if _, err := parseCommand(bad); CodeOf(err) != CodeInvalidCommand {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// Review pass 4, HIGH 1 probe: without an echo, write 35.5; the desk moves
// to 50. Until the core's re-read every write is refused against the 35.5
// in flight; after one re-read pass 49 (and lowering) works again. Before,
// the desk push dropped the driver's mark and the set lived forever.
func TestSafety_InFlightSetRecoversAfterOneRereadPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		if err := e.sim.SurfaceSet("/headamp/03/gain", osc.Float(89.0/144)); err != nil { // +32.5 dB
			t.Fatal(err)
		}
		e.startLive(t)
		e.net.SetDropFrom(consoleIP, true) // the echo of 35.5 is lost
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":35.5`)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		e.net.SetDropFrom(consoleIP, false)
		time.Sleep(500 * time.Millisecond)
		if err := e.sim.SurfaceSet("/headamp/03/gain", osc.Float(124.0/144)); err != nil { // the desk: +50 dB
			t.Fatal(err)
		}
		synctest.Wait()
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":49`)); CodeOf(err) != CodeGainBound {
			t.Fatalf("49 with 35.5 in flight: %v", err)
		}
		time.Sleep(40 * time.Second) // one background re-read pass (15 s ticker, paced reads)
		synctest.Wait()
		for _, db := range []string{"49", "45"} {
			if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":`+db)); err != nil {
				t.Fatalf("%s after the re-read: %v", db, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}

// Review pass 4, MEDIUM 3: a track shorter than 50 pt is not a phone
// control; the parser refuses it.
func TestHoldBegin_TrackLengthFloor(t *testing.T) {
	hb := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":1},"field":"fader"},"trackLengthPt":TRACK,"origin":{"kind":"gesture"}}`
	for track, ok := range map[string]bool{"50": true, "49.9": false, "1": false, "0.001": false} {
		_, err := parseCommand(strings.Replace(hb, "TRACK", track, 1))
		if ok != (err == nil) || (!ok && CodeOf(err) != CodeInvalidCommand) {
			t.Errorf("trackLengthPt %s: %v", track, err)
		}
	}
}
