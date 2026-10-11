// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
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

func FuzzParseXinfo(f *testing.F) {
	ok, _ := osc.AppendString(nil, "/xinfo", osc.String("192.168.1.62"), osc.String("X32-02-4A-53"),
		osc.String("X32"), osc.String("3.04")) // X32-F10 example
	f.Add(ok)
	f.Add([]byte("/xinfo\x00\x00,s\x00\x00a\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		var m osc.Message
		if osc.Decode(b, &m) != nil {
			return
		}
		_, _ = parseXinfo(&m) // must not panic
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

func TestDBToFader_NearestStepAtSegmentBreak(t *testing.T) {
	// X32-F41: the PDF table puts -30 dB at step 256; int(f*1023.5) would give 255.
	f, _ := dbToFader(-30)
	if k := math.Round(float64(f) * faderSteps); k != 256 {
		t.Fatalf("-30 dB is step %v, want 256", k)
	}
}
