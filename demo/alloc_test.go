// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package demo

import "testing"

// One meter tick (levels, mix read, Main L/R, publish to the simulator)
// allocates nothing in the demo's own code, so a running demo adds no garbage
// per frame beyond what the simulator itself does.
func TestAnimatorStep_DoesNotAllocate(t *testing.T) {
	sim, _, err := newConsole(FamilyX32, law, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := newAnimator(sim, law, families[FamilyX32], &lastMeters{inputs: make([]float64, 0, x32Inputs)}, nil)
	tick := 0.0
	if n := testing.AllocsPerRun(100, func() { tick += 0.05; a.step(tick) }); n != 0 {
		t.Fatalf("step allocates %v times", n)
	}
}

func BenchmarkAnimatorStep(b *testing.B) {
	for _, fam := range []string{FamilyX32, FamilyXAir} {
		b.Run(fam, func(b *testing.B) {
			sim, out, err := newConsole(fam, law, nil)
			if err != nil {
				b.Fatal(err)
			}
			a := newAnimator(sim, law, families[fam], out, nil)
			b.ReportAllocs()
			tick := 0.0
			for b.Loop() {
				tick += 0.05
				a.step(tick)
			}
		})
	}
}
