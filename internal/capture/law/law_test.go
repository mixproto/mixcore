// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package law

import (
	"errors"
	"math"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// xairDB is the documented X Air level law (mixcore docs/protocols/xair.md,
// XA-F40): four straight segments, 0.0 is -inf.
func xairDB(f float64) float64 {
	switch {
	case f <= 0:
		return math.Inf(-1)
	case f >= 0.5:
		return 40*f - 30
	case f >= 0.25:
		return 80*f - 50
	case f >= 0.0625:
		return 160*f - 70
	default:
		return 480*f - 90
	}
}

// step is the wire value a 1024-step fader sends for dB (XA-F41: k/1023).
func step(k int) float64 { return float64(k) / 1023 }

// The four surface readings an operator takes: -60, -30, -10 and +10 dB at
// the fader steps of the xair.md reference table (k = 64, 256, 512, 1023).
var xairFour = []Point{{step(64), -60}, {step(256), -30}, {step(512), -10}, {step(1023), 10}}

const segmentTolerance = 0.01 // relative, on slopes and intercepts

func TestFitRecoversXAirFaderLawFromFourPoints(t *testing.T) {
	best, all, err := Fit(xairFour, DefaultTolerance)
	if err != nil {
		t.Fatal(err)
	}
	if best.Kind != Piecewise || len(all) != 3 {
		t.Fatalf("best = %s (of %d)", best, len(all))
	}
	want := []Segment{{Slope: 160, Intercept: -70}, {Slope: 80, Intercept: -50}, {Slope: 40, Intercept: -30}}
	if len(best.Segments) != len(want) {
		t.Fatalf("segments = %+v", best.Segments)
	}
	for i, w := range want {
		s := best.Segments[i]
		if !near(s.Slope, w.Slope) || !near(s.Intercept, w.Intercept) {
			t.Fatalf("segment %d = %.4f*x %+.4f, want %v*x %+v", i, s.Slope, s.Intercept, w.Slope, w.Intercept)
		}
	}
	// Every reference point of xair.md between -60 and +10 dB (exact f column).
	for _, f := range []float64{0.0625, 0.125, 0.1875, 0.25, 0.375, 0.5, 0.625, 0.75, 0.875, 1} {
		if got, want := best.Eval(f), xairDB(f); math.Abs(got-want) > 0.05 {
			t.Errorf("Eval(%v) = %.3f dB, documented %.3f dB", f, got, want)
		}
	}
	if !strings.Contains(best.String(), "piecewise linear, 3 segment(s)") {
		t.Fatalf("String = %s", best)
	}
}

func near(got, want float64) bool { return math.Abs(got-want) <= segmentTolerance*math.Abs(want) }

func TestFitFloorFromMinusInf(t *testing.T) {
	pts := append([]Point{{0, math.Inf(-1)}}, xairFour...)
	best, _, err := Fit(pts, DefaultTolerance)
	if err != nil {
		t.Fatal(err)
	}
	if !best.HasFloor || best.FloorX != 0 || !math.IsInf(best.Eval(0), -1) || !strings.Contains(best.String(), "x <= 0 is -inf") {
		t.Fatalf("law = %s", best)
	}
}

func TestFitPrefersSimpleLaws(t *testing.T) {
	linear := []Point{{0, -32768}, {1000, 1000}, {500, -15884}}
	if l, _, err := Fit(linear, 1); err != nil || l.Kind != Linear || math.Abs(l.A-33.768) > 1e-3 {
		t.Fatalf("linear: %s, %v", l, err)
	}
	var amp []Point
	for _, x := range []float64{0.01, 0.1, 0.5, 1} {
		amp = append(amp, Point{x, 20 * math.Log10(x)})
	}
	if l, _, err := Fit(amp, DefaultTolerance); err != nil || l.Kind != Log10 || math.Abs(l.A-20) > 1e-9 {
		t.Fatalf("log10: %s, %v", l, err)
	}
}

func TestFitRejects(t *testing.T) {
	tests := []struct {
		name string
		pts  []Point
		want error
	}{
		{"one point", []Point{{0.5, -10}}, ErrTooFewPoints},
		{"only -inf", []Point{{0, math.Inf(-1)}, {0.1, math.Inf(-1)}}, ErrTooFewPoints},
		{"NaN", []Point{{math.NaN(), 0}, {1, 1}}, ErrBadPoints},
		{"two values at one x", []Point{{0.5, -10}, {0.5, -9}, {1, 10}}, ErrBadPoints},
		{"-inf above a finite point", []Point{{0.6, math.Inf(-1)}, {0.5, -10}, {1, 10}}, ErrBadPoints},
	}
	for _, tc := range tests {
		if _, _, err := Fit(tc.pts, 0); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}
