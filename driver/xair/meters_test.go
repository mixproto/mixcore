// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver/internal/xosc"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

func meterPacket(tb testing.TB) []byte {
	tb.Helper()
	pkt, err := osc.AppendString(nil, meterBank1, osc.Blob(bank1Blob(40, map[int]int16{0: 0, 1: -4608, 36: -256})))
	if err != nil {
		tb.Fatal(err)
	}
	return pkt
}

func BenchmarkMeterSink(b *testing.B) {
	pkt := meterPacket(b)
	sink := xosc.NewMeterSink(meterCodec{}, &meters.Slot{})
	sink.SwitchOn(true)
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		if !sink.Handle(pkt, now) {
			b.Fatal("not recognised as a meter packet")
		}
	}
}
