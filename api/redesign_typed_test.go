// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// AC-RD-T03 (redesign CC-5, core-api v0.5 rule 9): a typed raise of more
// than 6 dB without confirmed is ERR_NEEDS_CONFIRM on every level, writes
// nothing, and goes through with confirmed; a raise of 6 dB or less and
// any lowering never ask; from -inf every typed value asks.
func TestRedesign_TypedRaiseConfirmOnEveryLevel(t *testing.T) {
	levels := map[string]string{ // param -> console address, each at -20 dB
		`{"strip":{"kind":"input","index":1},"field":"fader"}`:              "/ch/01/mix/fader",
		`{"strip":{"kind":"main"},"field":"fader"}`:                         "/lr/mix/fader",
		`{"strip":{"kind":"bus","index":2},"field":"fader"}`:                "/bus/2/mix/fader",
		`{"strip":{"kind":"dca","index":1},"field":"fader"}`:                "/dca/1/fader",
		`{"strip":{"kind":"input","index":4},"field":"sendLevel","bus":2}`: "/ch/04/mix/02/level",
	}
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		for _, addr := range levels {
			if err := e.sim.SurfaceSet(addr, osc.Float(0.375)); err != nil { // -20 dB (XA-F40)
				t.Fatalf("%s: %v", addr, err)
			}
		}
		startWith(t, e, `{}`) // also Vox1 -6 dB, Gain 3 +32.5 dB
		_ = e.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.375))
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		for param := range levels {
			assertTypedRules(t, e, param, -20, -10)
		}
		assertTypedRules(t, e, gain3, 32.5, 42.5)
		if err := e.eng.Apply(cmdJSON("set", `{"strip":{"kind":"bus","index":3},"field":"fader"}`, `,"value":-50,"typed":true`)); CodeOf(err) != CodeNeedsConfirm {
			t.Fatalf("typed -50 dB from -inf: %v", err)
		}
	})
}

// assertTypedRules: from cur, a typed raise to big (more than 6 dB) asks
// and writes nothing; +4 dB, a lowering and the confirmed raise are written.
func assertTypedRules(t *testing.T, e *xairEnv, param string, cur, big float64) {
	t.Helper()
	before := len(e.sim.Writes())
	if err := e.eng.Apply(cmdJSON("set", param, fmt.Sprintf(`,"value":%v,"typed":true`, big))); CodeOf(err) != CodeNeedsConfirm {
		t.Fatalf("%s typed %v from %v: %v, want %s", param, big, cur, err, CodeNeedsConfirm)
	}
	synctest.Wait()
	if n := len(e.sim.Writes()) - before; n != 0 {
		t.Fatalf("%s: %d writes from a refused typed raise", param, n)
	}
	for _, v := range []float64{cur + 4, cur - 10} { // +4: no confirm; lowering never asks
		if err := e.eng.Apply(cmdJSON("set", param, fmt.Sprintf(`,"value":%v,"typed":true`, v))); err != nil {
			t.Fatalf("%s typed %v: %v", param, v, err)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
	}
	if err := e.eng.Apply(cmdJSON("set", param, fmt.Sprintf(`,"value":%v,"typed":true,"confirmed":true`, big))); err != nil {
		t.Fatalf("%s confirmed %v: %v", param, big, err)
	}
	time.Sleep(100 * time.Millisecond)
	synctest.Wait()
	if n := len(e.sim.Writes()) - before; n != 3 {
		t.Fatalf("%s: %d writes, want 3", param, n)
	}
}
