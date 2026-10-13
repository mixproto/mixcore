// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

const dbTolerance = 1e-6

func approxDB(a, b float64) bool {
	if math.IsInf(a, -1) || math.IsInf(b, -1) {
		return math.IsInf(a, -1) && math.IsInf(b, -1)
	}
	return math.Abs(a-b) <= dbTolerance
}

func f32Hex(f float32) string {
	return hex.EncodeToString(binary.BigEndian.AppendUint32(nil, math.Float32bits(f)))
}

// TestFaderWire_ReferenceTable pins the D5 bytes of wing.md section 4.
func TestFaderWire_ReferenceTable(t *testing.T) {
	cases := []struct {
		db   float64
		wire string
	}{
		{math.Inf(-1), "c3100000"}, {-60, "c2700000"}, {-30, "c1f00000"}, {-10, "c1200000"},
		{-3, "c0400000"}, {0, "00000000"}, {3, "40400000"}, {10, "41200000"},
	}
	for _, tc := range cases {
		f, err := faderToWire(tc.db)
		if err != nil || f32Hex(f) != tc.wire {
			t.Errorf("faderToWire(%v) = %s, %v; want %s", tc.db, f32Hex(f), err, tc.wire)
		}
		back, err := faderFromWire(f)
		if err != nil || !approxDB(back, tc.db) {
			t.Errorf("faderFromWire(%s) = %v, %v", tc.wire, back, err)
		}
	}
}

func TestFaderWire_ClampAndReject(t *testing.T) {
	if f, _ := faderToWire(12); f != 10 {
		t.Fatalf("+12 dB -> %v, want 10", f)
	}
	if f, _ := faderToWire(-200); f != -144 {
		t.Fatalf("-200 dB -> %v, want -144", f)
	}
	if _, err := faderToWire(math.NaN()); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("NaN: %v", err)
	}
	for _, bad := range []float32{float32(math.NaN()), float32(math.Inf(1)), 10.5} {
		if _, err := faderFromWire(bad); !errors.Is(err, errBadWire) {
			t.Fatalf("faderFromWire(%v): %v", bad, err)
		}
	}
	if db, _ := faderFromWire(-150); !math.IsInf(db, -1) {
		t.Fatal("below -144 is -inf")
	}
}

// TestDisplayLaw_PublishedRawPoints: WG-F61's raw values (OSC) lie on the
// display law; +10 dB = 1.0 is [TO-VERIFY] item 9 but the law's top.
func TestDisplayLaw_PublishedRawPoints(t *testing.T) {
	law := faderLaw{}
	for _, p := range []struct{ raw, db float64 }{{0.675, -3}, {0.7, -2}, {0.725, -1}, {0.75, 0}, {0.825, 3}, {0.85, 4}, {1, 10}, {0, math.Inf(-1)}} {
		if got := law.DBFromPosition(p.raw); !approxDB(got, p.db) {
			t.Errorf("DBFromPosition(%v) = %v, want %v", p.raw, got, p.db)
		}
		if got := law.PositionFromDB(p.db); math.Abs(got-p.raw) > 1e-9 {
			t.Errorf("PositionFromDB(%v) = %v, want %v", p.db, got, p.raw)
		}
	}
	if law.PositionFromDB(-144) != 0 || law.PositionFromDB(math.NaN()) != 0 || !math.IsInf(law.DBFromPosition(math.NaN()), -1) {
		t.Fatal("bottom and NaN")
	}
}

// FuzzFaderLaw: the display law is monotonic and round-trips.
func FuzzFaderLaw(f *testing.F) {
	f.Add(0.3, 0.6)
	f.Fuzz(func(t *testing.T, a, b float64) {
		law := faderLaw{}
		if math.IsNaN(a) || math.IsNaN(b) {
			return
		}
		if a < b && law.DBFromPosition(a) > law.DBFromPosition(b) {
			t.Fatalf("not monotonic at %v, %v", a, b)
		}
		if a > 0 && a < 1 {
			if back := law.PositionFromDB(law.DBFromPosition(a)); math.Abs(back-a) > 1e-9 && law.DBFromPosition(a) > faderDisplayMinDB {
				t.Fatalf("round trip %v -> %v", a, back)
			}
		}
	})
}

// TestGainLaw_ReferenceTable: wing.md section 4, LCL (k of 19) and AES (k of 97).
func TestGainLaw_ReferenceTable(t *testing.T) {
	lcl, ok1 := gainLawOf(wingproto.Def{Type: wingproto.TypeLinFloat, Min: -2.5, Max: 45, Steps: 20})
	aes, ok2 := gainLawOf(wingproto.Def{Type: wingproto.TypeLinFloat, Min: -3, Max: 45.5, Steps: 98})
	if !ok1 || !ok2 || lcl.step != 2.5 || aes.step != 0.5 {
		t.Fatalf("laws %+v %+v", lcl, aes)
	}
	for _, c := range []struct {
		law  gainLaw
		db   float64
		wire string
	}{
		{lcl, -2.5, "c0200000"}, {lcl, 0, "00000000"}, {lcl, 10, "41200000"}, {lcl, 20, "41a00000"}, {lcl, 30, "41f00000"},
		{aes, -3, "c0400000"}, {aes, 0, "00000000"}, {aes, 10, "41200000"}, {aes, 20, "41a00000"}, {aes, 30, "41f00000"}, {aes, 45, "42340000"},
	} {
		f, err := c.law.toWire(c.db)
		if err != nil || f32Hex(f) != c.wire {
			t.Errorf("gain %v dB -> %s, %v; want %s", c.db, f32Hex(f), err, c.wire)
		}
	}
	if f, _ := lcl.toWire(11); f != 10 {
		t.Fatalf("11 dB snaps to %v, want 10 (grid -2.5 + 2.5 k)", f)
	}
	if f, _ := lcl.toWire(11.4); f != 12.5 {
		t.Fatalf("11.4 dB snaps to %v, want 12.5", f)
	}
	if _, err := lcl.toWire(45); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("the wire maximum is never written: %v", err)
	}
	if f, _ := lcl.toWire(-10); f != -2.5 {
		t.Fatalf("below the minimum clamps: %v", f)
	}
	if _, ok := gainLawOf(wingproto.Def{Type: wingproto.TypeLogFloat, Min: 1, Max: 2, Steps: 3}); ok {
		t.Fatal("only a linear law is a gain law")
	}
}

func TestDCATags(t *testing.T) {
	cases := map[string]uint32{
		"": 0, "#D1": 1, "#D16": 1 << 15, "#D2,#D12": 1<<1 | 1<<11, "#D3 #D4": 1<<2 | 1<<3,
		"#Drums": 0, "#D17": 0, "#D0": 0, "#D1x": 1, "x#D10": 1 << 9, "#D123": 0,
	}
	for tags, want := range cases {
		if got := dcaMaskFromTags(tags); got != want {
			t.Errorf("dcaMaskFromTags(%q) = %b, want %b", tags, got, want)
		}
	}
}

func TestParseIdentity_DocExamples(t *testing.T) {
	id, err := ParseIdentity([]byte("WING,192.168.1.71,PGM,ngc-full,NO_SERIAL,1.07.2-40-g1b1b292b:develop"))
	if err != nil || id.Name != "PGM" || id.Model != "ngc-full" || id.Firmware != "1.07.2-40-g1b1b292b:develop" {
		t.Fatalf("vendor example: %+v %v", id, err)
	}
	id, err = ParseIdentity([]byte("WING,192.168.1.19,WING-PP-93892216,wing-compact,01XXXUX06X3AEX,3.0.5-0-g0c2b9d4a:release"))
	if err != nil || id.Model != "wing-compact" {
		t.Fatalf("WS6 example: %+v %v", id, err)
	}
	for _, bad := range []string{"WING?", "WING,1,2,3", "XR18,1,2,3,4,5", "WING,a,b,c,d,e,f"} {
		if _, err := ParseIdentity([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzParseIdentity(f *testing.F) {
	f.Add([]byte("WING,192.168.1.71,PGM,ngc-full,NO_SERIAL,1.07.2-40-g1b1b292b:develop"))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseIdentity(b) })
}

func TestModelAndFirmwareFacts(t *testing.T) {
	for m, want := range map[string]int{"ngc-full": 8, "wing-bk": 8, "wing-fullsize": 8, "": 8, "wing-rack": 24, "wing-compact": 24} {
		if got := localInputsFor(m); got != want {
			t.Errorf("localInputsFor(%q) = %d", m, got)
		}
	}
	for fw, want := range map[string]int{"3.0.6-0-g0:release": 16, "3.1.0-0-g0:release": 24, "3.1.1": 24, "4.0": 24, "2.1": 16, "x": 16, "": 16, "3.10": 24} {
		if got := clientsFor(fw); got != want {
			t.Errorf("clientsFor(%q) = %d, want %d", fw, got, want)
		}
	}
}
