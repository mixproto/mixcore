// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// Redesign CC-13 (core-api v0.5, meter-frame.md): the bus masters of bank
// 2 (values 0..15, X32-F55) are levels 32..47, between the inputs and Main
// L/R, which stay the last two (48, 49); the capabilities say where.
func TestMeters_BusMastersBetweenInputsAndMain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.sim.SetInputLevels([]float32{1})
		r.sim.SetBusLevels([]float32{0, 0, 0.5}) // bus 3 at -6 dBFS
		r.sim.SetMainLevels(1, 0.5)
		sess := r.connect()
		r.run(sess)
		if c := sess.Capabilities(); c.BusMeterFirst != 32 || c.BusMeters != 16 {
			t.Fatalf("capabilities: bus meters %d from level %d", c.BusMeters, c.BusMeterFirst)
		}
		if err := sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		var fr meters.Frame
		if !r.slot.CopyLatest(&fr) || fr.Count != 50 {
			t.Fatalf("frame count = %d, want 50", fr.Count)
		}
		if fr.Levels[0] != 0 || math.Abs(float64(fr.Levels[34])+6.0206) > 1e-3 || !math.IsInf(float64(fr.Levels[32]), -1) ||
			fr.Levels[48] != 0 || math.Abs(float64(fr.Levels[49])+6.0206) > 1e-3 {
			t.Fatalf("levels: in1 %v, bus 1 %v, bus 3 %v, main %v %v", fr.Levels[0], fr.Levels[32], fr.Levels[34], fr.Levels[48], fr.Levels[49])
		}
	})
}
