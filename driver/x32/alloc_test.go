// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package x32

import (
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver/internal/xosc"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

// The meter path and the fader write encode have a 0 allocs/op budget.
func TestZeroAlloc_MeterPath(t *testing.T) {
	pkt := meterPacket(t)
	sink := xosc.NewMeterSink(meterCodec{}, &meters.Slot{})
	sink.SwitchOn(true)
	now := time.Now()
	if n := testing.AllocsPerRun(200, func() { sink.Handle(pkt, now) }); n != 0 {
		t.Fatalf("meter packet handling allocates %v times, budget 0", n)
	}
	bus, _ := osc.AppendString(nil, meterBank2, osc.Blob(bank2Blob(0.5, 1)))
	if n := testing.AllocsPerRun(200, func() { sink.Handle(bus, now) }); n != 0 {
		t.Fatalf("bank 2 packet handling allocates %v times, budget 0", n)
	}
}
