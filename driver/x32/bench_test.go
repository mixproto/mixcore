// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

func meterPacket(tb testing.TB) []byte {
	tb.Helper()
	pkt, err := osc.AppendString(nil, meterBank1, osc.Blob(meterBlob(1, 0.5, 0.25)))
	if err != nil {
		tb.Fatal(err)
	}
	return pkt
}

func BenchmarkMeterSink(b *testing.B) {
	pkt := meterPacket(b)
	sink := newMeterSink(&meters.Slot{})
	sink.enabled.Store(true)
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		if !sink.handle(pkt, now) {
			b.Fatal("not recognised as a meter packet")
		}
	}
}

func BenchmarkEncodeFaderWrite(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		f, err := dbToFader(-12.5)
		if err != nil {
			b.Fatal(err)
		}
		if buf, err = osc.AppendString(buf[:0], "/ch/01/mix/fader", osc.Float(f)); err != nil {
			b.Fatal(err)
		}
	}
}
