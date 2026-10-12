// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/mixproto/mixcore/meters"
)

// meterBlob builds a bank 1 blob (X32-F53) with the given linear input levels.
func meterBlob(levels ...float32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 96)
	for i := range 96 {
		var v float32
		if i < len(levels) {
			v = levels[i]
		}
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func FuzzDecodeMeters1(f *testing.F) {
	f.Add(meterBlob(1, 0.5))
	// Blob of the PDF's /meters/6 reply (X32-F53): count 4, four floats.
	pdf, _ := hex.DecodeString("04000000fd1d2137fdff7f3f0000803f6ebbd534")
	f.Add(pdf)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, blob []byte) {
		var fr meters.Frame
		if err := decodeMeters1(blob, &fr); err != nil {
			return
		}
		if fr.Count < 0 || fr.Count > inputChannels {
			t.Fatalf("count %d out of range", fr.Count)
		}
		for _, l := range fr.Levels[:fr.Count] {
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 1) {
				t.Fatalf("level %v must be finite or -inf", l)
			}
		}
	})
}

// bank2Blob builds a bank 2 blob (X32-F55): 49 values, Main L/R at 22/23.
func bank2Blob(l, r float32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 49)
	for i := range 49 {
		var v float32
		switch i {
		case 22:
			v = l
		case 23:
			v = r
		}
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func FuzzDecodeMainLR(f *testing.F) {
	f.Add(bank2Blob(1, 0.5))
	f.Add(bank2Blob(float32(math.NaN()), float32(math.Inf(1))))
	f.Add([]byte{0x18, 0, 0, 0})
	f.Fuzz(func(t *testing.T, blob []byte) {
		var fr meters.Frame
		if err := decodeMainLR(blob, &fr); err != nil {
			return
		}
		for _, l := range fr.Levels[mainLLevel : mainLLevel+2] {
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 1) {
				t.Fatalf("level %v must be finite or -inf", l)
			}
		}
	})
}

func TestDecodeMeters1_PDFBlob(t *testing.T) {
	pdf, _ := hex.DecodeString("04000000fd1d2137fdff7f3f0000803f6ebbd534")
	var fr meters.Frame
	if err := decodeMeters1(pdf, &fr); err != nil || fr.Count != 4 {
		t.Fatalf("PDF blob: count %d, %v", fr.Count, err)
	}
	if math.Abs(float64(fr.Levels[2])) > 1e-6 { // third value 0x3f800000 = 1.0 = 0 dBFS
		t.Fatalf("level 2 = %v, want 0 dBFS", fr.Levels[2])
	}
	if err := decodeMeters1([]byte{8, 0, 0, 0, 1, 2, 3, 4}, &fr); err == nil {
		t.Fatal("count larger than the blob must be rejected")
	}
	if err := decodeMeters1([]byte{1}, &fr); err == nil {
		t.Fatal("short blob must be rejected")
	}
}
