// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package osc

import "testing"

const faderAddress = "/ch/01/mix/fader"

func encodeFaderSet(buf []byte) ([]byte, error) {
	return AppendString(buf[:0], faderAddress, Float(0.75))
}

func BenchmarkAppendFaderSet(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		if buf, err = encodeFaderSet(buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeMetersBlob(b *testing.B) {
	pkt := mustHex(b, goldenVectors[len(goldenVectors)-1].hex)
	var m Message
	b.ReportAllocs()
	for b.Loop() {
		if err := Decode(pkt, &m); err != nil {
			b.Fatal(err)
		}
	}
}
