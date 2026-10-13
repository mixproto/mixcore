// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// CR-27: the touch paths of dybyskz-mix docs/design/acceptance/phone-safety.md
// ("Touch paths") as data, shared by the core's limiter tests and every UI's
// gesture-machine tests. This file builds them; the committed fixture must
// match (go test ./api -run GesturePaths -update-paths rewrites it).

var updatePaths = flag.Bool("update-paths", false, "rewrite contracts/core-api/fixtures/gesture-paths.json")

const (
	pathSampleMs = 8 // "samples every 8 ms"
	fixturePath  = "../contracts/core-api/fixtures/gesture-paths.json"
)

type pathPoint struct {
	T int     `json:"t"` // ms since touch-down
	X float64 `json:"x"` // pt / dp, +x = right
	Y float64 `json:"y"` // pt / dp, +y = up (screen direction)
}

type pathVariant struct {
	Orientation string      `json:"orientation"` // any, portrait, landscape
	Points      []pathPoint `json:"points"`
}

type gesturePath struct {
	ID           string        `json:"id"`
	Motion       string        `json:"motion"`
	Why          string        `json:"why"`
	LargeContact bool          `json:"largeContact,omitempty"` // contact larger than touch.largeContact
	Start        *pathStart    `json:"start,omitempty"`        // v0.5: where a Gain-row path starts (absent: on the control)
	Pointer      string        `json:"pointer,omitempty"`      // v0.5: "mouse" for a pointer path (absent: touch)
	Variants     []pathVariant `json:"variants"`
}

// pathStart places the touch-down of a Gain-row path (redesign CC-8,
// AC-RD-G01, G02): on the wheel, from its left end or at its centre, or
// some pt from a screen edge (where only the steppers are, SR-32).
type pathStart struct {
	Control    string   `json:"control"`              // gainWheel or screenEdge
	FromLeftPt *float64 `json:"fromLeftPt,omitempty"` // gainWheel: pt from the wheel's left end; absent: its centre
	Edge       string   `json:"edge,omitempty"`       // screenEdge: left or right
	FromEdgePt float64  `json:"fromEdgePt,omitempty"` // screenEdge: pt from that edge
}

type pathsFile struct {
	Version     int           `json:"version"`
	Description string        `json:"description"`
	Units       string        `json:"units"`
	SampleMs    int           `json:"sampleMs"`
	Paths       []gesturePath `json:"paths"`
}

// sample builds points every 8 ms from 0 to dur ms (inclusive) of f(u), u in 0..1.
func sample(dur int, f func(u float64) (x, y float64)) []pathPoint {
	var out []pathPoint
	for t := 0; ; t += pathSampleMs {
		t = min(t, dur)
		u := 1.0
		if dur > 0 {
			u = float64(t) / float64(dur)
		}
		x, y := f(u)
		out = append(out, pathPoint{T: t, X: round1(x), Y: round1(y)})
		if t == dur {
			return out
		}
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// line is a straight move to (dx, dy) in dur ms, after a hold of holdMs.
func line(holdMs, dur int, dx, dy float64) []pathPoint {
	pts := sample(holdMs, func(float64) (float64, float64) { return 0, 0 })
	for _, p := range sample(dur, func(u float64) (float64, float64) { return dx * u, dy * u })[1:] {
		pts = append(pts, pathPoint{T: p.T + holdMs, X: p.X, Y: p.Y})
	}
	return pts
}

func any1(pts []pathPoint) []pathVariant { return []pathVariant{{Orientation: "any", Points: pts}} }

// buildPaths returns P1..P8, P1D and P1R..P6R as specified (phone-safety
// acceptance, "Touch paths").
func buildPaths() pathsFile {
	arc := sample(300, func(u float64) (float64, float64) { // straight up 20, then curving right to (60, 90)
		if u <= 0.2 {
			return 0, 20 * u / 0.2
		}
		s := (u - 0.2) / 0.8
		return 60 * s * s, 20 + 70*s
	})
	circle := sample(600, func(u float64) (float64, float64) { // radius 40 around (0, 40), clockwise from the grab point
		a := -math.Pi/2 - 2*math.Pi*u
		return 40 * math.Cos(a), 40 + 40*math.Sin(a)
	})
	edge := func(top float64) []pathPoint { // up past the edge (the OS keeps reporting), then back down 100
		return sample(500, func(u float64) (float64, float64) {
			if u <= 0.6 {
				return 0, (top + 50) * u / 0.6
			}
			return 0, top + 50 - 100*(u-0.6)/0.4
		})
	}
	ps := []gesturePath{
		{ID: "P1", Motion: "up 60 in 40 ms", Why: "a slip", Variants: any1(line(0, 40, 0, 60))},
		{ID: "P2", Motion: "up to the top edge of the control in 150 ms (portrait 400, landscape 180)", Why: "a fast push from the middle",
			Variants: []pathVariant{{"portrait", line(0, 150, 0, 400)}, {"landscape", line(0, 150, 0, 180)}}},
		{ID: "P3", Motion: "straight up 20, then curving right, ending 90 up and 60 right, 300 ms", Why: "diagonal summing", Variants: any1(arc)},
		{ID: "P4", Motion: "hold still 1000 ms, then up 200 in 250 ms", Why: "the founder's motion", Variants: any1(line(1000, 250, 0, 200))},
		{ID: "P5", Motion: "one full circle of radius 40 around a point 40 above the grab point, 600 ms, clockwise", Why: "angle mapping", Variants: any1(circle)},
		{ID: "P6", Motion: "up past the screen's top edge, then back down 100, 500 ms", Why: "absolute mapping on re-entry",
			Variants: []pathVariant{{"portrait", edge(400)}, {"landscape", edge(180)}}},
		{ID: "P7", Motion: "hold still 2000 ms", Why: "a hold read as go to the end", Variants: any1(line(2000, 0, 0, 0))},
		{ID: "P8", Motion: "a large contact lands on the cap and moves up 100 in 200 ms", Why: "the hand holding the phone",
			LargeContact: true, Variants: any1(line(0, 200, 0, 100))},
		{ID: "P1D", Motion: "down 60 in 40 ms", Why: "the emergency pull must never be stopped", Variants: any1(line(0, 40, 0, -60))},
	}
	for _, p := range ps[:6] { // P1R..P6R: rotated 90 degrees clockwise, up becomes right
		r := gesturePath{ID: p.ID + "R", Motion: p.Motion + ", rotated 90 degrees clockwise", Why: p.Why + " on a horizontal control"}
		for _, v := range p.Variants {
			pts := make([]pathPoint, len(v.Points))
			for i, q := range v.Points {
				pts[i] = pathPoint{T: q.T, X: q.Y, Y: round1(-q.X) + 0} // + 0 turns -0 into 0
			}
			r.Variants = append(r.Variants, pathVariant{Orientation: v.Orientation, Points: pts})
		}
		ps = append(ps, r)
	}
	ps = append(ps, gainWheelPaths()...)
	return pathsFile{Version: contractVersion, SampleMs: pathSampleMs, Paths: ps,
		Description: "CR-27 touch paths of dybyskz-mix docs/design/acceptance/phone-safety.md and (v0.5, CC-8) the Gain wheel paths of acceptance/redesign.md; the finger lifts after the last point.",
		Units:       "t in ms since touch-down; x, y in pt (Apple) or dp (Android) relative to the grab point; +x right, +y up"}
}

// gainWheelPaths are the Gain wheel paths of the redesign (CC-8, AC-RD-G01,
// G02): strokes inside and out of the wheel's frame, a mouse drag, the
// longest stroke and system back-swipes starting near a screen edge.
func gainWheelPaths() []gesturePath {
	at := func(x float64) *pathStart { return &pathStart{Control: "gainWheel", FromLeftPt: &x} }
	centre := &pathStart{Control: "gainWheel"}
	drift := sample(400, func(u float64) (float64, float64) { // right 400; after 100 pt it drifts up 40
		x := 400 * u
		return x, 40 * max(0, x-100) / 300
	})
	out := []gesturePath{
		{ID: "G1", Motion: "from 5 pt inside the wheel's left end, right 230 in 307 ms (750 pt/s), inside the frame",
			Why: "a deliberate stroke is never capped (AC-RD-G01)", Start: at(5), Variants: any1(line(0, 307, 230, 0))},
		{ID: "G2", Motion: "from the wheel's centre, right 400 in 400 ms, beyond the frame",
			Why: "leaving the frame ends the stroke (AC-RD-G02)", Start: centre, Variants: any1(line(0, 400, 400, 0))},
		{ID: "G2D", Motion: "from the wheel's centre, right 400 in 400 ms, drifting up 40 after the first 100",
			Why: "a drift out of the frame ends the stroke (AC-RD-G02)", Start: centre, Variants: any1(drift)},
		{ID: "G2M", Motion: "mouse: from the wheel's centre, right 600 and back 600 with the button held, 1000 ms",
			Why: "the pointer leaving the frame ends the stroke; coming back writes nothing (AC-RD-G02)", Start: centre, Pointer: "mouse",
			Variants: any1(sample(1000, func(u float64) (float64, float64) { return 600 * (1 - math.Abs(2*u-1)), 0 }))},
		{ID: "G3", Motion: "from 1 pt inside the wheel's left end to 1 pt inside its right end (iPhone 6.1 inch portrait: 238), 300 ms",
			Why: "the longest stroke: at most 5 dB on every device (AC-RD-G02)", Start: at(1), Variants: any1(line(0, 300, 238, 0))},
	}
	for _, edge := range []string{"left", "right"} {
		dx := 200.0
		if edge == "right" {
			dx = -200
		}
		for _, d := range []float64{10, 20, 30, 40} {
			out = append(out, gesturePath{ID: fmt.Sprintf("E%.0f%s", d, map[string]string{"left": "L", "right": "R"}[edge]),
				Motion: fmt.Sprintf("a back swipe on the Gain row from %.0f pt off the %s screen edge, 200 inward in 150 ms", d, edge),
				Why:    "the screen edges hold only tap targets: zero Gain writes (AC-RD-G02)",
				Start:  &pathStart{Control: "screenEdge", Edge: edge, FromEdgePt: d}, Variants: any1(line(0, 150, dx, 0))})
		}
	}
	return out
}

func TestGesturePaths_FixtureIsCurrent(t *testing.T) {
	b, err := json.MarshalIndent(buildPaths(), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if *updatePaths {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(b) {
		t.Fatalf("%s is out of date: go test ./api -run GesturePaths -update-paths", fixturePath)
	}
}

// loadPaths reads the committed fixture: the replay runs on the data the
// UIs use, not on this file's builder.
func loadPaths(t *testing.T) pathsFile {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var f pathsFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}
