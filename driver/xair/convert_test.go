// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// faderReference is the table of xair.md section 4 (XA-F40, XA-F41): the dB
// asked for, the step k of 1023 the driver must send, and the dB the mixer
// value reads back as.
var faderReference = []struct {
	db     float64
	step   int
	readDB float64
}{
	{10, 1023, 10}, {5, 895, 5.00}, {0, 767, -0.01}, {-5, 639, -5.01}, {-10, 512, -9.98}, {-20, 384, -19.97},
	{-30, 256, -29.98}, {-40, 192, -39.97}, {-50, 128, -49.98}, {-60, 64, -59.99}, {-70, 43, -69.82},
	{-80, 21, -80.15}, {math.Inf(-1), 0, math.Inf(-1)},
}

func approxDB(a, b float64) bool {
	if math.IsInf(a, -1) || math.IsInf(b, -1) {
		return math.IsInf(a, -1) && math.IsInf(b, -1)
	}
	return math.Abs(a-b) <= 0.005
}

// Every reference point goes through Apply to the mixer and back: the wire
// value is the documented step, and the read-back dB is the documented one.
func TestFaderLaw_ReferenceTableEndToEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		for i, tc := range faderReference {
			ch := i%inputChannels + 1
			if err := sess.Apply(driver.Command{Param: ref(ch, model.FieldFader), Value: level(t, tc.db), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			pc, ok := nextOf[driver.ParamChanged](events, time.Second)
			got, _ := pc.Value.DB()
			v, _ := r.sim.Value(fmt.Sprintf("/ch/%02d/mix/fader", ch))
			if k := math.Round(float64(v.Float) * 1023); !ok || int(k) != tc.step || !approxDB(got, tc.readDB) {
				t.Errorf("%v dB: wire step %v (want %d), echo %v dB (want %v)", tc.db, k, tc.step, got, tc.readDB)
			}
		}
		r.assertWritesAudited()
	})
}

// The UI law (FaderPosition/FaderDB) is the continuous X32 law (XA-F40).
func TestFaderLaw_UIPositions(t *testing.T) {
	law := New(Config{}).FaderLaw()
	for _, tc := range []struct{ db, pos float64 }{
		{10, 1}, {0, 0.75}, {-10, 0.5}, {-30, 0.25}, {-60, 0.0625}, {math.Inf(-1), 0}, {-90, 0}, {20, 1},
	} {
		if got := law.PositionFromDB(tc.db); math.Abs(got-tc.pos) > 1e-12 {
			t.Errorf("PositionFromDB(%v) = %v, want %v", tc.db, got, tc.pos)
		}
	}
	if !math.IsInf(law.DBFromPosition(0), -1) || law.DBFromPosition(1) != 10 || law.DBFromPosition(math.NaN()) != math.Inf(-1) {
		t.Fatal("ends of the law")
	}
}

// XA-F51, XA-F52: int16 in 1/256 dB; -32768 is silence (-inf).
func TestMeterValue_Conversion(t *testing.T) {
	for _, tc := range []struct {
		wire int16
		dbfs float64
	}{
		{0, 0}, {-4608, -18}, {-256, -1}, {256, 1}, {-1, -1.0 / 256}, {-32767, -32767.0 / 256},
		{math.MaxInt16, 32767.0 / 256}, {math.MinInt16, math.Inf(-1)},
	} {
		if got := float64(dbfsFromWire(tc.wire)); got != tc.dbfs {
			t.Errorf("%d -> %v dBFS, want %v", tc.wire, got, tc.dbfs)
		}
	}
}

// bank1Blob builds a blob as XA-F51 describes: count, then count int16 LE.
func bank1Blob(count int, values map[int]int16) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(count))
	for i := range count {
		v, ok := values[i]
		if !ok {
			v = silence
		}
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}

func TestDecodeBank1_LayoutAndRejections(t *testing.T) {
	var fr meters.Frame
	if err := decodeBank1(bank1Blob(40, map[int]int16{0: 0, 15: -2560, 16: 0, 36: -512, 37: -768}), &fr); err != nil {
		t.Fatal(err)
	}
	if fr.Levels[0] != 0 || fr.Levels[15] != -10 || fr.Levels[16] != -2 || fr.Levels[17] != -3 || !math.IsInf(float64(fr.Levels[1]), -1) {
		t.Fatalf("levels %v", fr.Levels[:18])
	}
	for name, blob := range map[string][]byte{
		"short":                    {1, 2},
		"count beyond the blob":    bank1Blob(40, nil)[:40],
		"no Main R (count 37)":     bank1Blob(37, nil),
		"huge count":               binary.LittleEndian.AppendUint32(nil, math.MaxUint32),
		"float32 instead (XA-F52)": binary.LittleEndian.AppendUint32(bank1Blob(20, nil)[:4], 0),
	} {
		if err := decodeBank1(blob, &fr); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// FuzzDecodeBank1: any bytes are decoded or rejected, never a panic, and
// every accepted level is a finite dB or -inf, never NaN.
func FuzzDecodeBank1(f *testing.F) {
	f.Add(bank1Blob(40, map[int]int16{0: 0, 36: -256}))
	f.Add(bank1Blob(38, nil))
	f.Add([]byte{40, 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		var fr meters.Frame
		if decodeBank1(b, &fr) != nil {
			return
		}
		for i := range meterLevels {
			if math.IsNaN(float64(fr.Levels[i])) || math.IsInf(float64(fr.Levels[i]), 1) {
				t.Fatalf("level %d = %v", i, fr.Levels[i])
			}
		}
	})
}

// FuzzMeterPacket runs whole packets through the codec as the reader does.
func FuzzMeterPacket(f *testing.F) {
	f.Add(meterPacket(f))
	f.Add([]byte("/meters/1\x00\x00\x00,b\x00\x00\x00\x00\x00\x04\x28\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, pkt []byte) {
		var fr meters.Frame
		c := meterCodec{}
		if bank, ok := c.Bank(pkt); ok {
			_ = c.Decode(bank, pkt, &fr) // a raw packet is not a blob: must reject or decode, never panic
		}
	})
}

func TestIsModel(t *testing.T) {
	for model, want := range map[string]bool{
		"XR18": true, "XR16": true, "XR12": true, "X18": true, "MR18": true, "MR12": true,
		"XR18V2": true, "X18V2": true, "MR18V2": true, "XR12W": true, "MR12V2": true,
		"X32": false, "M32": false, "X32RACK": false, "": false, "WING": false,
	} {
		if IsModel(model) != want {
			t.Errorf("IsModel(%q) = %t", model, !want)
		}
	}
}
