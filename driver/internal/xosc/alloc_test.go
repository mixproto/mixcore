// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package xosc

import (
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
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

// Core-api v0.4: a send write and the decode of its echo are hot paths of
// a send drag (go-core-standards section 9): no allocation.
func TestZeroAlloc_SendWriteAndEcho(t *testing.T) {
	p := sendParam(t, newParamTable(x32BusLayout), 4, 2, model.FieldSendLevel)
	v := mustLevel(t, -6)
	buf := make([]byte, 0, 64)
	if n := testing.AllocsPerRun(200, func() {
		a, _ := toWire(p, v)
		buf, _ = osc.AppendString(buf[:0], p.addr, a)
	}); n != 0 {
		t.Fatalf("send write encode allocates %v times, budget 0", n)
	}
	echo := osc.Float(0.6)
	if n := testing.AllocsPerRun(200, func() { _, _ = fromWire(p, echo) }); n != 0 {
		t.Fatalf("send echo decode allocates %v times, budget 0", n)
	}
}
