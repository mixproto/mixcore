// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package xosc

import (
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
)

func TestZeroAlloc_FaderWrite(t *testing.T) {
	buf := make([]byte, 0, 64)
	if n := testing.AllocsPerRun(200, func() {
		f, _ := dbToFader(-12.5)
		buf, _ = osc.AppendString(buf[:0], "/ch/01/mix/fader", osc.Float(f))
	}); n != 0 {
		t.Fatalf("fader write encode allocates %v times, budget 0", n)
	}
}
