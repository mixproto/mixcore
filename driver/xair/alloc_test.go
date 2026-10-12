// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package xair

import (
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver/internal/xosc"
	"github.com/mixproto/mixcore/meters"
)

func TestZeroAlloc_MeterPath(t *testing.T) {
	pkt := meterPacket(t)
	slot := &meters.Slot{}
	sink := xosc.NewMeterSink(meterCodec{}, slot)
	sink.SwitchOn(true)
	now := time.Now()
	if n := testing.AllocsPerRun(200, func() { sink.Handle(pkt, now) }); n != 0 {
		t.Fatalf("bank 1 packet handling allocates %v times, budget 0", n)
	}
	var fr meters.Frame
	if !slot.CopyLatest(&fr) || fr.Count != meterLevels || fr.Levels[1] != -18 || fr.Levels[mainLLevel] != -1 {
		t.Fatalf("frame %d levels %v", fr.Count, fr.Levels[:meterLevels])
	}
}
