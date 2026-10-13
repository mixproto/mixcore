// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// Redesign CC-3 (core-api v0.5 rule 8): on a console that does not echo a
// Gain write (X32-F79 unverified, sim NoGainPush), the driver re-reads that
// headamp at most 250 ms after the last write of a burst (the settle
// re-read); its reply is authoritative and flagged as our own write.

const settleBudget = 250 * time.Millisecond

// gainReads counts the reads of /headamp/000/gain since the connect.
func gainReads(sim *simx32.Sim, after time.Time) int {
	n := 0
	for _, e := range sim.Log() {
		if e.Class == simx32.ClassRead && e.Address == "/headamp/000/gain" && e.At.After(after) {
			n++
		}
	}
	return n
}

func TestSettle_UnechoedGainBurstIsReadBackWithin250ms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{NoGainPush: true})
		sess := r.connect()
		events, _ := r.run(sess)
		start := time.Now()
		for i := 1; i <= 4; i++ { // a burst at 40 per second
			if err := sess.Apply(driver.Command{Param: headamp(1, model.FieldGain), Value: level(t, float64(i)), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(25 * time.Millisecond)
		}
		last := time.Now().Add(-25 * time.Millisecond)
		ev, ok := nextOf[driver.ParamChanged](events, settleBudget)
		for ok && ev.Param != headamp(1, model.FieldGain) {
			ev, ok = nextOf[driver.ParamChanged](events, settleBudget)
		}
		if !ok || time.Since(last) > settleBudget {
			t.Fatalf("no settle report within 250 ms of the last write (%v)", time.Since(last))
		}
		if !ev.Authoritative || !ev.OwnWrite || !ev.Value.Equal(level(t, 4)) {
			t.Fatalf("settle report %+v, want our own +4 dB, authoritative", ev)
		}
		if n := gainReads(r.sim, start); n != 1 {
			t.Fatalf("%d settle reads for one burst, want 1", n)
		}
	})
}

// A console that echoes the write needs no settle read.
func TestSettle_EchoedGainIsNotReadBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		_, _ = r.run(sess)
		start := time.Now()
		if err := sess.Apply(driver.Command{Param: headamp(1, model.FieldGain), Value: level(t, 3), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if n := gainReads(r.sim, start); n != 0 {
			t.Fatalf("%d settle reads of an echoed write", n)
		}
	})
}
