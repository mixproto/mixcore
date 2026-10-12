// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// Half a fader step in the steepest segment (-90..-60 dB spans 480 dB per unit,
// one step is 1/1023): every quantized round trip lands within this.
const (
	wireTolerance = 0.5 / faderSteps
	dbTolerance   = 480.0 / faderSteps / 2
)

func approxDB(a, b, tol float64) bool {
	if math.IsInf(a, -1) || math.IsInf(b, -1) {
		return math.IsInf(a, -1) && math.IsInf(b, -1) // -Inf - -Inf is NaN, so compare explicitly
	}
	return math.Abs(a-b) <= tol
}

// Reference points from docs/protocols/x32.md X32-F40..F43.
func TestFaderLaw_ReferencePoints(t *testing.T) {
	tests := []struct {
		name  string
		wire  float32
		db    float64
		dbTol float64
	}{
		{"top of travel (X32-F40)", 1.0, 10, 0},
		{"PDF packet 0x3f5334cd is step 844 = +3 dB (X32-F43)", math.Float32frombits(0x3f5334cd), 3, 0.002},
		{"unity is 0.75 (X32-F43)", 0.75, 0, 0},
		{"segment break at -10 dB", 0.5, -10, 0},
		{"segment break at -30 dB", 0.25, -30, 0},
		{"segment break at -60 dB", 0.0625, -60, 0},
		{"step 1 is about -89.5 dB (X32-F42)", 1.0 / 1023, -89.53, 0.01},
		{"bottom is -oo (X32-F42)", 0, math.Inf(-1), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := faderToDB(tc.wire)
			if err != nil || !approxDB(got, tc.db, tc.dbTol+1e-9) {
				t.Fatalf("faderToDB(%v) = %v dB, %v; want %v dB", tc.wire, got, err, tc.db)
			}
			back, err := dbToFader(tc.db)
			if err != nil || math.Abs(float64(back-tc.wire)) > wireTolerance {
				t.Fatalf("dbToFader(%v) = %v, %v; want %v", tc.db, back, err, tc.wire)
			}
		})
	}
}

func TestDBToFader_ExactPDFBits(t *testing.T) {
	got, err := dbToFader(3)
	if err != nil || math.Float32bits(got) != 0x3f5334cd {
		t.Fatalf("dbToFader(+3) = %#08x, %v; want 0x3f5334cd from the PDF example", math.Float32bits(got), err)
	}
}

func TestFaderLaw_ClampsAndRejects(t *testing.T) {
	clamps := []struct {
		name string
		db   float64
		want float32
	}{
		{"+12 dB clamps to top", 12, 1},
		{"-200 dB clamps to off", -200, 0},
		{"exactly -90 dB is off", -90, 0},
		{"-inf is off", math.Inf(-1), 0},
		{"+inf clamps to top", math.Inf(1), 1},
	}
	for _, tc := range clamps {
		got, err := dbToFader(tc.db)
		if err != nil || got != tc.want {
			t.Fatalf("%s: dbToFader(%v) = %v, %v; want %v", tc.name, tc.db, got, err, tc.want)
		}
	}
	if _, err := dbToFader(math.NaN()); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("dbToFader(NaN) err = %v, want ErrInvalidValue", err)
	}
	if got, err := faderToDB(1.5); err != nil || got != faderMaxDB {
		t.Fatalf("faderToDB(1.5) = %v, %v; want +10", got, err)
	}
	if got, err := faderToDB(-0.5); err != nil || !math.IsInf(got, -1) {
		t.Fatalf("faderToDB(-0.5) = %v, %v; want -inf", got, err)
	}
	if _, err := faderToDB(float32(math.NaN())); !errors.Is(err, errBadWire) {
		t.Fatalf("faderToDB(NaN) err = %v, want errBadWire", err)
	}
}

func TestDBToFader_AlwaysOnAStep(t *testing.T) {
	for db := -95.0; db <= 11; db += 0.37 {
		f, err := dbToFader(db)
		if err != nil {
			t.Fatal(err)
		}
		k := float64(f) * faderSteps
		if math.Abs(k-math.Round(k)) > 1e-3 {
			t.Fatalf("dbToFader(%v) = %v is not a multiple of 1/1023 (X32-F41)", db, f)
		}
	}
}

// FuzzFaderLaw: the law is monotonic and a quantized round trip stays within half a step.
func FuzzFaderLaw(f *testing.F) {
	f.Add(float32(0.1), float32(0.2))
	f.Add(float32(0.0625), float32(0.25))
	f.Add(float32(0.75), float32(1))
	f.Fuzz(func(t *testing.T, a, b float32) {
		if math.IsNaN(float64(a)) || math.IsNaN(float64(b)) {
			return
		}
		lo, hi := min(a, b), max(a, b)
		dlo, err1 := faderToDB(lo)
		dhi, err2 := faderToDB(hi)
		if err1 != nil || err2 != nil {
			t.Fatalf("faderToDB errors: %v %v", err1, err2)
		}
		if dlo > dhi {
			t.Fatalf("not monotonic: faderToDB(%v)=%v > faderToDB(%v)=%v", lo, dlo, hi, dhi)
		}
		back, err := dbToFader(dlo)
		if err != nil {
			t.Fatal(err)
		}
		clamped := min(max(lo, 0), 1)
		if math.Abs(float64(back-clamped)) > wireTolerance+1e-6 {
			t.Fatalf("round trip %v -> %v dB -> %v", lo, dlo, back)
		}
	})
}

func TestFaderLaw_UIPositions(t *testing.T) {
	law := faderLaw{}
	tests := []struct {
		db  float64
		pos float64
	}{
		{10, 1}, {0, 0.75}, {-10, 0.5}, {-30, 0.25}, {-60, 0.0625}, {math.Inf(-1), 0}, {-120, 0}, {20, 1},
	}
	for _, tc := range tests {
		if got := law.PositionFromDB(tc.db); math.Abs(got-tc.pos) > 1e-9 {
			t.Fatalf("PositionFromDB(%v) = %v, want %v", tc.db, got, tc.pos)
		}
		if !math.IsInf(tc.db, -1) && tc.db >= faderMinDB && tc.db <= faderMaxDB {
			if got := law.DBFromPosition(tc.pos); !approxDB(got, tc.db, 1e-9) {
				t.Fatalf("DBFromPosition(%v) = %v, want %v", tc.pos, got, tc.db)
			}
		}
	}
	if got := law.PositionFromDB(math.NaN()); got != 0 {
		t.Fatalf("PositionFromDB(NaN) = %v, want 0", got)
	}
	if got := law.DBFromPosition(0); !math.IsInf(got, -1) {
		t.Fatalf("DBFromPosition(0) = %v, want -inf", got)
	}
	if got := law.DBFromPosition(math.NaN()); !math.IsInf(got, -1) {
		t.Fatalf("DBFromPosition(NaN) = %v, want -inf", got)
	}
	if got := law.DBFromPosition(3); got != faderMaxDB {
		t.Fatalf("DBFromPosition(3) = %v, want +10", got)
	}
}

// X32-F33, X32-F37: mix/on and dca/on 1 = on (not muted), 0 = muted.
// X32-F35: /config/mute 1 = group engaged (muting): the opposite polarity.
func TestSwitches_Polarity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		c     conv
		muted bool
		wire  int32
	}{
		{"channel muted", convOnSwitch, true, 0},
		{"channel on", convOnSwitch, false, 1},
		{"group engaged", convEngaged, true, 1},
		{"group released", convEngaged, false, 0},
	} {
		if got := switchToWire(tc.c, tc.muted); got != tc.wire {
			t.Errorf("%s: switchToWire = %d, want %d", tc.name, got, tc.wire)
		}
		if m, err := switchFromWire(tc.c, tc.wire); err != nil || m != tc.muted {
			t.Errorf("%s: switchFromWire(%d) = %v, %v", tc.name, tc.wire, m, err)
		}
	}
	for _, c := range []conv{convOnSwitch, convEngaged} {
		if _, err := switchFromWire(c, 2); !errors.Is(err, errBadWire) {
			t.Fatalf("switchFromWire(2) err = %v", err)
		}
	}
}

// X32-F36, X32-F38: membership bitmaps are read-only and range-checked.
func TestMembership_Bitmaps(t *testing.T) {
	tbl := newParamTable(1)
	p, _ := tbl.byAddress([]byte("/ch/01/grp/mute"))
	if v, err := fromWire(p, osc.Int(63)); err != nil || !v.Equal(model.Mask(63)) {
		t.Fatalf("grp/mute 63 = %v, %v", v, err)
	}
	if _, err := fromWire(p, osc.Int(64)); !errors.Is(err, errBadWire) {
		t.Fatalf("grp/mute 64 err = %v", err)
	}
	d, _ := tbl.byAddress([]byte("/ch/01/grp/dca"))
	if _, err := fromWire(d, osc.Int(255)); err != nil {
		t.Fatalf("grp/dca 255 err = %v", err)
	}
	if _, err := toWire(p, model.Mask(1)); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("membership write err = %v", err)
	}
}

func TestName_Conversion(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"plain", "Kick In", false},
		{"twelve chars is the max (X32-F31)", "123456789012", false},
		{"thirteen chars rejected", "1234567890123", true},
		{"non-ASCII rejected", "Vocal é", true},
		{"control char rejected", "a\tb", true},
		{"empty allowed", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := nameToWire(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("nameToWire(%q) err = %v", tc.in, err)
			}
			if err != nil && !errors.Is(err, model.ErrInvalidValue) {
				t.Fatalf("error must wrap ErrInvalidValue: %v", err)
			}
		})
	}
	if got := nameFromWire([]byte("ok\xff")); !strings.HasPrefix(got, "ok") || got == "ok\xff" {
		t.Fatalf("nameFromWire must sanitise invalid UTF-8, got %q", got)
	}
}

func TestToWireFromWire_PerField(t *testing.T) {
	ps := newParamTable(2)
	fader, _ := ps.byRef(model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldFader})
	mute, _ := ps.byRef(model.ParamRef{Strip: model.MainRef, Field: model.FieldMute})
	name, _ := ps.byRef(model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 2}, Field: model.FieldName})

	lv, _ := model.Level(0)
	if a, err := toWire(fader, lv); err != nil || a.Type != osc.TypeFloat || a.Float != float32(767)/faderSteps {
		t.Fatalf("fader toWire = %+v, %v", a, err)
	}
	if a, err := toWire(mute, model.Bool(true)); err != nil || a.Type != osc.TypeInt || a.Int != 0 {
		t.Fatalf("mute toWire = %+v, %v", a, err)
	}
	nv, _ := model.Text("Vox")
	if a, err := toWire(name, nv); err != nil || a.Type != osc.TypeString || string(a.Bytes) != "Vox" {
		t.Fatalf("name toWire = %+v, %v", a, err)
	}
	if _, err := toWire(fader, model.Bool(true)); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("kind mismatch err = %v", err)
	}

	if v, err := fromWire(fader, osc.Float(0.5)); err != nil || !v.Equal(mustLevel(t, -10)) {
		t.Fatalf("fader fromWire = %v, %v", v, err)
	}
	if v, err := fromWire(mute, osc.Int(1)); err != nil || !v.Equal(model.Bool(false)) {
		t.Fatalf("mute fromWire = %v, %v", v, err)
	}
	if _, err := fromWire(fader, osc.Int(1)); !errors.Is(err, errBadWire) {
		t.Fatalf("wrong wire type err = %v", err)
	}
}

func TestParamTable_Addresses(t *testing.T) {
	ps := newParamTable(inputChannels)
	// Per input: name, fader, mute, grp/mute, grp/dca; main: fader, mute, grp/mute; 6 groups; 8 DCAs.
	if want := inputChannels*5 + 3 + muteGroupCount + dcaCount; len(ps.all) != want {
		t.Fatalf("table has %d params, want %d", len(ps.all), want)
	}
	want := map[string]model.ParamRef{
		"/ch/01/mix/fader":   {Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldFader},
		"/ch/32/mix/on":      {Strip: model.StripRef{Kind: model.StripInput, Index: 32}, Field: model.FieldMute},
		"/ch/09/config/name": {Strip: model.StripRef{Kind: model.StripInput, Index: 9}, Field: model.FieldName},
		"/main/st/mix/fader": {Strip: model.MainRef, Field: model.FieldFader},
		"/main/st/mix/on":    {Strip: model.MainRef, Field: model.FieldMute},
		"/ch/05/grp/mute":    {Strip: model.StripRef{Kind: model.StripInput, Index: 5}, Field: model.FieldMuteGroups},
		"/ch/05/grp/dca":     {Strip: model.StripRef{Kind: model.StripInput, Index: 5}, Field: model.FieldDCAs},
		"/main/st/grp/mute":  {Strip: model.MainRef, Field: model.FieldMuteGroups},
		"/config/mute/6":     {Strip: model.StripRef{Kind: model.StripMuteGroup, Index: 6}, Field: model.FieldMute},
		"/dca/8/on":          {Strip: model.StripRef{Kind: model.StripDCA, Index: 8}, Field: model.FieldMute},
	}
	for addr, ref := range want {
		p, ok := ps.byAddress([]byte(addr))
		if !ok || p.ref != ref {
			t.Fatalf("byAddress(%s) = %+v, %v; want %v", addr, p, ok, ref)
		}
		q, ok := ps.byRef(ref)
		if !ok || q.addr != addr {
			t.Fatalf("byRef(%v) = %+v, %v; want %s", ref, q, ok, addr)
		}
	}
	if _, ok := ps.byAddress([]byte("/ch/33/mix/fader")); ok {
		t.Fatal("channel 33 does not exist")
	}
}

func mustLevel(t *testing.T, db float64) model.Value {
	t.Helper()
	v, err := model.Level(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
