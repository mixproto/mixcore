// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// AC-RD-G03 through the API (redesign CC-3), against sim/xair in "no Gain
// echo" mode: a hold that sends 20 one-step candidates at 40 per second is
// written in order, no atCap before the range end, no write more than 6 dB
// from the previous one; the settle re-reads that follow never end the hold
// and never move the shown value back, even when the drag's latest write
// was lost and the read-back is an earlier write; a value at the surface that none of
// the drag's writes had ends it; a single non-hold write of +7 dB from the
// console value is still ERR_GAIN_BOUND (SR-31).
func TestRedesign_GainDragWithoutEchoNeverStalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{NoGainPush: true})
		startWith(t, e, `{}`)
		gestureResult(t, e, cmdJSON("holdBegin", gain3, ""))
		for i := 1; i <= 20; i++ {
			e.net.SetDropFrom("10.0.0.2", i == 10) // write 10 is lost on the way
			r := gestureResult(t, e, cmdJSON("set", gain3, `,"value":`+levelText(32.5+float64(i))))
			if r.State != "free" || !r.Written {
				t.Fatalf("step %d: %+v", i, r)
			}
			synctest.Wait()
			e.net.SetDropFrom("10.0.0.2", false)
			time.Sleep(25 * time.Millisecond)
			if i == 10 {
				time.Sleep(300 * time.Millisecond) // the finger rests: the settle re-read reads write 9 back
			}
		}
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()
		assertGainWritesStepUp(t, e, 19, 10)
		assertNoBackwardGain(t, e)
		if err := e.sim.SurfaceSet("/headamp/03/gain", osc.Float(130.0/144)); err != nil { // +53 dB at the desk
			t.Fatal(err)
		}
		synctest.Wait()
		if !strings.Contains(strings.Join(e.rec.raw, "\n"), `"type":"externalChangeOnHeld","param":{"strip":{"kind":"headamp","index":3},"field":"gain"}`) {
			t.Fatal("a desk value none of the writes had must end the hold")
		}
		gestureResult(t, e, cmdJSON("holdEnd", gain3, ""))
		if err := e.eng.Apply(cmdJSON("set", gain3, `,"value":60`)); CodeOf(err) != CodeGainBound {
			t.Fatalf("+7 dB outside a hold: %v", err)
		}
	})
}

// assertGainWritesStepUp: n writes of headamp 3, each 1 dB above the last
// (2 dB across the lost write number lost).
func assertGainWritesStepUp(t *testing.T, e *xairEnv, n, lost int) {
	t.Helper()
	var got []float64
	for _, w := range e.sim.Writes() {
		if w.Address == "/headamp/03/gain" {
			got = append(got, gainMinDB+gainSpanDB*float64(w.Arg.Float))
		}
	}
	if len(got) != n {
		t.Fatalf("%d Gain writes %v, want %d", len(got), got, n)
	}
	for i := 1; i < len(got); i++ {
		want := 1.0
		if i == lost-1 {
			want = 2
		}
		if d := got[i] - got[i-1]; d < want-0.01 || d > want+0.01 {
			t.Fatalf("write %d: %.2f after %.2f", i, got[i], got[i-1])
		}
	}
}

// assertNoBackwardGain: no externalChangeOnHeld, and the headamp's
// published values never fall while the finger only moved up.
func assertNoBackwardGain(t *testing.T, e *xairEnv) {
	t.Helper()
	prev := 32.5
	for _, m := range e.rec.all() {
		for _, ev := range m.Events {
			if ev.Type == "externalChangeOnHeld" {
				t.Fatalf("the drag's own writes ended the hold: %+v", ev)
			}
		}
		if m.Changes == nil {
			continue
		}
		for _, c := range *m.Changes {
			if c.Param.Strip.Kind == "headamp" && c.Param.Strip.Index == 3 && c.Param.Field == "gain" {
				v := c.Value.(float64)
				if v < prev {
					t.Fatalf("the shown Gain moved back from %.1f to %.1f", prev, v)
				}
				prev = v
			}
		}
	}
	if prev < 52.4 {
		t.Fatalf("the settle re-read never published the drag's end: %.1f", prev)
	}
}
