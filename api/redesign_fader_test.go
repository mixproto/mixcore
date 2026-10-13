// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Redesign CC-1 (core-api v0.5, README rule 7, AC-RD-F01, AC-RD-F02): the
// slip guard is retired. A v0.3 settings file with slipGuard on is still
// accepted, setSlipGuard is accepted, and neither stops a fader; openWall
// is ERR_UNSUPPORTED; atWall and wallUpDB are never sent.

func gestureResult(t *testing.T, e *xairEnv, cmd string) gestureResultJSON {
	t.Helper()
	out, err := e.eng.Gesture(cmd)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	var r gestureResultJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "wallUpDB") || strings.Contains(out, "atWall") || strings.Contains(out, `"opensByRest":true`) {
		t.Fatalf("%s: a retired slip-guard field in %s", cmd, out)
	}
	return r
}

func TestRedesign_SlipGuardSettingNeverStopsAFader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{"slipGuard":true}`) // a v0.3 phone settings file
		if err := e.eng.Apply(cmdJSON("setSlipGuard", "", `,"value":true`)); err != nil {
			t.Fatalf("setSlipGuard: %v", err)
		}
		gestureResult(t, e, cmdJSON("holdBegin", vox1, `,"trackLengthPt":490`))
		var last gestureResultJSON
		for _, db := range []float64{-3, 0, 3, 6, 10} { // P2: a deliberate push to the top
			last = gestureResult(t, e, cmdJSON("set", vox1, `,"value":`+levelText(db)))
			if last.State != "free" || math.Abs(last.Value.(float64)-db) > 1e-9 {
				t.Fatalf("candidate %v: %+v, want free at the candidate", db, last)
			}
		}
		if err := e.eng.Apply(cmdJSON("openWall", vox1, "")); CodeOf(err) != CodeUnsupported {
			t.Fatalf("openWall: %v, want %s", err, CodeUnsupported)
		}
		gestureResult(t, e, cmdJSON("holdEnd", vox1, ""))
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if v, _ := e.sim.Value("/ch/01/mix/fader"); v.Float != 1 {
			t.Fatalf("mixer fader %v, want +10 dB (1.0)", v.Float)
		}
	})
}
