// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
)

// X32-F72, XA-F116: an echo matches a write within half a step of that
// parameter's own law: 0.5/1023 for a fader, 0.5/144 for mic Gain, 0.5/64
// for line Gain. The fader tolerance on a Gain echo would take a console
// that rounds differently for a desk change and end the user's gesture.
func TestSameWire_HalfAStepOfTheParamsOwnLaw(t *testing.T) {
	fader := param{conv: convLevel}
	mic := param{conv: convGain, gainSteps: 144}
	line := param{conv: convGain, gainSteps: 64}
	for _, c := range []struct {
		name string
		p    param
		d    float32
		want bool
	}{
		{"fader within", fader, 0.0004, true},
		{"fader beyond", fader, 0.0006, false},
		{"mic within", mic, 0.0034, true},
		{"mic beyond", mic, 0.0036, false},
		{"line within", line, 0.0077, true},
		{"line beyond", line, 0.0080, false},
	} {
		for _, d := range []float32{c.d, -c.d} {
			if got := sameWire(c.p, osc.Float(0.5), osc.Float(0.5+d)); got != c.want {
				t.Errorf("%s (%+v): %v", c.name, d, got)
			}
		}
	}
	if !sameWire(param{conv: convFlag}, osc.Int(1), osc.Int(1)) || sameWire(param{conv: convFlag}, osc.Int(1), osc.Int(0)) {
		t.Fatal("ints compare exactly")
	}
}
