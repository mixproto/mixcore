// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"math"
	"slices"
	"strings"
	"testing"
)

// The three legacy mapping doubles of AC-PS-06 (test code only, never
// shipped). Each maps a whole path to the values it would set, as a fraction
// 0..1 of the control's range, starting from p0. No core limiter applies.
type legacyDouble func(pts []pathPoint, p0, length float64, horizontal bool) []float64

// absoluteDouble (a): the position under the finger on the control's range.
func absoluteDouble(pts []pathPoint, p0, length float64, horizontal bool) []float64 {
	out := make([]float64, 0, len(pts))
	for _, p := range pts {
		along := p.Y
		if horizontal {
			along = p.X
		}
		out = append(out, clamp01(p0+along/length))
	}
	return out
}

// angleDouble (b): the finger's angle around the knob centre, a 270 degree
// sweep clockwise from 225 degrees, wrapping past 0/360. In the 90 degree
// gap below the knob it clamps to the nearer end, as clamping angle mappings
// do; the grab point (straight below) maps to the minimum.
func angleDouble(pts []pathPoint, _, _ float64, _ bool) []float64 {
	out := make([]float64, 0, len(pts))
	for _, p := range pts {
		if math.Hypot(p.X, p.Y-knobCentreY) < 1 {
			continue
		}
		a := math.Atan2(p.Y-knobCentreY, p.X) * 180 / math.Pi
		cw := math.Mod(knobMinAngleDeg-a+720, 360)
		switch {
		case cw <= knobSweepDeg:
			out = append(out, cw/knobSweepDeg)
		case cw < (knobSweepDeg+360)/2:
			out = append(out, 1) // just past the maximum
		default:
			out = append(out, 0) // just before the minimum, the grab point included
		}
	}
	return out
}

// speedupDouble (c): change = (up + right) x (full range per 300 pt) x
// (1 + speed / 250), as Mixing Station documents for its knobs.
func speedupDouble(pts []pathPoint, p0, _ float64, _ bool) []float64 {
	out, v := []float64{p0}, p0
	for i := 1; i < len(pts); i++ {
		dx, dy := pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y
		dt := float64(pts[i].T-pts[i-1].T) / 1000
		speed := 0.0
		if dt > 0 {
			speed = math.Hypot(dx, dy) / dt
		}
		v = clamp01(v + (dx+dy)*rangePerPt*(1+speed/speedupPtS))
		out = append(out, v)
	}
	return out
}

func clamp01(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

func reachesMax(vals []float64) bool {
	for _, v := range vals {
		if v >= 1 {
			return true
		}
	}
	return false
}

// variantFor picks the orientation's variant, or the one for any orientation.
func variantFor(p gesturePath, orientation string) pathVariant {
	for _, v := range p.Variants {
		if v.Orientation == orientation {
			return v
		}
	}
	return p.Variants[0]
}

type geometry struct {
	orientation   string
	track, gainAt float64 // gainAt: the v0.3 Gain bar of the legacy doubles
	wheel         float64 // the Gain wheel (SR-32)
}

var phone = []geometry{{"portrait", trackPortrait, barPortrait, wheelPortrait}, {"landscape", trackLandscape, barLandscape, wheelLandscape}}

// AC-PS-06: every legacy double reaches the range maximum on at least one
// path (a on P2, b on P5, c on P4), for the fader and for Gain; our mapping
// with the CR-22 limiter reaches it on none.
func TestTouchPaths_NegativeControl(t *testing.T) {
	paths := loadPaths(t)
	faderP0 := 0.6 // -6 dB on the x32/xair law (X32-F40, XA-F40)
	gainP0 := (32.5 - gainMinDB) / gainSpanDB
	for name, tc := range map[string]struct {
		double legacyDouble
		must   string
	}{"absolute": {absoluteDouble, "P2"}, "angle": {angleDouble, "P5"}, "speedup": {speedupDouble, "P4"}} {
		for _, g := range phone {
			hit := map[string]bool{}
			for _, p := range paths.Paths {
				if p.LargeContact || strings.HasPrefix(p.ID, "P1D") || p.ID == "P8" || p.Start != nil {
					continue
				}
				pts := variantFor(p, g.orientation).Points
				horizontal := strings.HasSuffix(p.ID, "R")
				if reachesMax(tc.double(pts, faderP0, g.track, false)) && !horizontal {
					hit["fader "+p.ID] = true
				}
				if reachesMax(tc.double(pts, gainP0, g.gainAt, true)) && horizontal {
					hit["gain "+p.ID] = true
				}
			}
			t.Logf("%s double, %s: reaches the maximum on %v", name, g.orientation, sortedKeys(hit))
			if !hit["fader "+tc.must] {
				t.Errorf("%s %s: the fader double does not reach +10 dB on %s; the path set is too weak (hits %v)", name, g.orientation, tc.must, hit)
			}
			if !hit["gain "+tc.must+"R"] && !anyGain(hit) {
				t.Errorf("%s %s: the Gain double never reaches +60 dB (hits %v)", name, g.orientation, hit)
			}
		}
	}
}

func anyGain(hit map[string]bool) bool {
	for k := range hit {
		if strings.HasPrefix(k, "gain ") {
			return true
		}
	}
	return false
}

// AC-PS-01 (amended 2026-10-13), AC-RD-F01: through our mapping and the
// core, every fader write follows the finger through the fader law: P2 (a
// deliberate push) ends at +10 dB with no stop, P1 moves 60 pt worth,
// holds and palms write nothing, P1D falls with the finger, and no write
// leaves the range.
func TestTouchPaths_FaderNeverJumps(t *testing.T) {
	paths := loadPaths(t)
	for _, g := range phone {
		p0 := 0.6 // -6 dB on the x32/xair law (X32-F40, XA-F40)
		for _, p := range onControl(paths.Paths) {
			v := variantFor(p, g.orientation)
			r := replay(t, ctlFader, g.track, v, p.LargeContact)
			if r.stops != 0 || r.max() > 10+writeTol {
				t.Errorf("%s %s: %d stops, highest %.2f dB", g.orientation, p.ID, r.stops, r.max())
			}
			checkFollowsFinger(t, g, p, v, r, p0)
		}
	}
}

// checkFollowsFinger: the last write is the finger's own value on the law
// (constant ratio, no acceleration, no jump), per path.
func checkFollowsFinger(t *testing.T, g geometry, p gesturePath, v pathVariant, r run, p0 float64) {
	t.Helper()
	switch p.ID {
	case "P7", "P8":
		if len(r.writes) != 0 {
			t.Errorf("%s %s: %d writes, want none", g.orientation, p.ID, len(r.writes))
		}
		return
	case "P4":
		if r.firstAt >= 0 && r.firstAt < 1000 {
			t.Errorf("%s P4: a write at %d ms, during the hold", g.orientation, r.firstAt)
		}
	case "P3", "P5", "P6":
		return // the axis lock decides; covered by the negative control
	}
	if !strings.HasSuffix(p.ID, "R") && len(r.writes) > 0 {
		end := v.Points[len(v.Points)-1].Y
		want := lawDB(math.Min(math.Max(p0+end/g.track, 0), 1))
		if math.Abs(r.last()-want) > writeTol {
			t.Errorf("%s %s: ends at %.2f dB, the finger is at %.2f dB", g.orientation, p.ID, r.last(), want)
		}
	}
}

// lawDB is the x32/xair fader law (X32-F40, XA-F40) for a test oracle.
func lawDB(pos float64) float64 {
	switch {
	case pos <= 0:
		return math.Inf(-1)
	case pos >= 0.5:
		return pos*40 - 30
	case pos >= 0.25:
		return pos*80 - 50
	case pos >= 0.0625:
		return pos*160 - 70
	default:
		return pos*480 - 90
	}
}

// AC-PS-02, AC-PS-06 (amended 2026-10-13), AC-RD-G02: on the Gain wheel
// (frame rule, 1 dB per 48 pt, grabbed at its centre), vertical paths
// never write; a horizontal path changes Gain by at most floor(half the
// wheel / 48) dB, mirrored too, never reaches the range end, and P1R is
// exactly one step.
func TestTouchPaths_GainNeverJumps(t *testing.T) {
	paths := loadPaths(t)
	for _, g := range phone {
		limit := math.Floor(g.wheel / 2 / encoderPitchPt)
		for _, p := range onControl(paths.Paths) {
			v := variantFor(p, g.orientation)
			r := replay(t, ctlGain, g.wheel, v, p.LargeContact)
			if !strings.HasSuffix(p.ID, "R") {
				if len(r.writes) != 0 {
					t.Errorf("%s %s on the Gain wheel: %d writes, want none (vertical drags scroll)", g.orientation, p.ID, len(r.writes))
				}
				continue
			}
			if len(r.writes) > 0 && (r.max() > 32.5+limit+writeTol || r.max() >= 59.5) {
				t.Errorf("%s %s: Gain up to %.1f (limit +%.0f)", g.orientation, p.ID, r.max(), limit)
			}
			if p.ID == "P1R" && (len(r.writes) != 1 || math.Abs(r.last()-33.5) > writeTol) {
				t.Errorf("%s P1R: writes %v, want exactly +1 dB", g.orientation, r.writes)
			}
			mirrored := pathVariant{Orientation: v.Orientation}
			for _, q := range v.Points {
				mirrored.Points = append(mirrored.Points, pathPoint{T: q.T, X: -q.X, Y: q.Y})
			}
			if m := replay(t, ctlGain, g.wheel, mirrored, false); len(m.writes) > 0 && m.min() < 32.5-limit-writeTol {
				t.Errorf("%s %s mirrored: Gain down to %.1f", g.orientation, p.ID, m.min())
			}
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// onControl keeps the paths that start on the control itself (P1..P8, P1D,
// P1R..P6R); the Gain-row paths (Start set) have their own proof.
func onControl(ps []gesturePath) []gesturePath {
	var out []gesturePath
	for _, p := range ps {
		if p.Start == nil {
			out = append(out, p)
		}
	}
	return out
}

// iPhone 6.1 inch portrait Gain row (fader-and-gain.md 5.1): screen 393 pt,
// the wheel 240 pt wide, 76 pt from each screen edge.
const (
	screenPortrait  = 393.0
	wheelEdgeOffset = 76.0
)

// grabXOf places a Gain-row path's touch-down in wheel coordinates.
func grabXOf(s *pathStart, wheel float64) float64 {
	switch {
	case s.Control == "screenEdge" && s.Edge == "left":
		return s.FromEdgePt - wheelEdgeOffset
	case s.Control == "screenEdge":
		return screenPortrait - s.FromEdgePt - wheelEdgeOffset
	case s.FromLeftPt != nil:
		return *s.FromLeftPt
	}
	return wheel / 2
}

// AC-RD-G01, AC-RD-G02 (CC-8): on the iPhone portrait wheel a stroke from
// near the left end writes four 1 dB steps; leaving the frame (touch or
// mouse, drift included) ends the stroke after at most floor(120 / 48) dB
// and coming back writes nothing; the longest stroke is at most 5 dB; a
// back swipe from 10 to 40 pt off either edge writes no Gain at all.
func TestTouchPaths_GainWheelFrameRule(t *testing.T) {
	want := map[string]struct{ writes, maxUp int }{
		"G1": {4, 4}, "G2": {2, 2}, "G2D": {2, 2}, "G2M": {2, 2}, "G3": {4, 5},
	}
	for _, p := range loadPaths(t).Paths {
		if p.Start == nil {
			continue
		}
		v := variantFor(p, "portrait")
		r := replayFrom(t, ctlGain, wheelPortrait, grabXOf(p.Start, wheelPortrait), v, false)
		w, gainRow := want[p.ID]
		if !gainRow { // E10L..E40R: back swipes over the steppers
			if len(r.writes) != 0 {
				t.Errorf("%s: %d Gain writes from a back swipe, want none", p.ID, len(r.writes))
			}
			continue
		}
		if len(r.writes) != w.writes || r.max() > 32.5+float64(w.maxUp)+writeTol || r.stops != 0 {
			t.Errorf("%s: writes %v (%d stops), want %d writes up to +%d dB", p.ID, r.writes, r.stops, w.writes, w.maxUp)
		}
		if p.ID == "G1" && math.Abs(r.last()-36.5) > writeTol {
			t.Errorf("G1 ends at %.1f, want +36.5 (AC-RD-G01)", r.last())
		}
	}
}
