// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package law fits a candidate value law to a few known points: wire values
// observed in the capture (x) against the engineering values the operator
// read on the console surface (y, e.g. dB; -inf allowed). It tries, in
// order, a straight line, a logarithmic law y = a*log10(x) + b, and a
// piecewise-linear table through the points, and picks the first one whose
// largest residual is within the tolerance. The result is a candidate for a
// [CAPTURE] fact, never a fact by itself: more points make it trustworthy.
package law

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Kind is the shape of a law.
type Kind string

// Law kinds, in the order Fit prefers them.
const (
	Linear    Kind = "linear"
	Log10     Kind = "log10"
	Piecewise Kind = "piecewise"
)

// DefaultTolerance is the largest residual (in y units) a law may have.
const DefaultTolerance = 0.1

// Point is one known pair. Y may be -Inf (fader off).
type Point struct{ X, Y float64 }

// Segment is y = Slope*x + Intercept on [X0, X1].
type Segment struct{ X0, X1, Slope, Intercept float64 }

// Law is a fitted law.
type Law struct {
	Kind        Kind
	A, B        float64   // Linear: y = A*x + B; Log10: y = A*log10(x) + B
	Segments    []Segment // Piecewise
	HasFloor    bool      // x <= FloorX is -inf
	FloorX      float64
	MaxResidual float64
	Points      int
}

// Errors.
var (
	ErrTooFewPoints = errors.New("law: need at least two points with a finite value")
	ErrBadPoints    = errors.New("law: inconsistent points")
)

// Fit returns the preferred law and every law that could be fitted.
func Fit(points []Point, tol float64) (Law, []Law, error) {
	if tol <= 0 {
		tol = DefaultTolerance
	}
	finite, floor, hasFloor, err := split(points)
	if err != nil {
		return Law{}, nil, err
	}
	base := Law{HasFloor: hasFloor, FloorX: floor, Points: len(points)}
	var all []Law
	if l, ok := fitLinear(finite, base, false); ok {
		all = append(all, l)
	}
	if l, ok := fitLinear(finite, base, true); ok {
		all = append(all, l)
	}
	all = append(all, fitPiecewise(finite, base, tol))
	for _, l := range all {
		if l.MaxResidual <= tol {
			return l, all, nil
		}
	}
	return all[len(all)-1], all, nil
}

// split validates points: finite ones sorted by x (distinct x), and the
// largest x with y = -inf, which must lie below every finite point.
func split(points []Point) ([]Point, float64, bool, error) {
	var finite []Point
	floor, hasFloor := math.Inf(-1), false
	for _, p := range points {
		switch {
		case math.IsNaN(p.X) || math.IsInf(p.X, 0) || math.IsNaN(p.Y) || math.IsInf(p.Y, 1):
			return nil, 0, false, fmt.Errorf("%w: (%v, %v)", ErrBadPoints, p.X, p.Y)
		case math.IsInf(p.Y, -1):
			floor, hasFloor = math.Max(floor, p.X), true
		default:
			finite = append(finite, p)
		}
	}
	slices.SortFunc(finite, func(a, b Point) int { return cmp.Compare(a.X, b.X) })
	for i := 1; i < len(finite); i++ {
		if finite[i].X == finite[i-1].X && finite[i].Y != finite[i-1].Y {
			return nil, 0, false, fmt.Errorf("%w: x=%v has two values", ErrBadPoints, finite[i].X)
		}
	}
	finite = slices.CompactFunc(finite, func(a, b Point) bool { return a.X == b.X })
	if len(finite) < 2 {
		return nil, 0, false, ErrTooFewPoints
	}
	if hasFloor && floor >= finite[0].X {
		return nil, 0, false, fmt.Errorf("%w: -inf at x=%v above a finite point", ErrBadPoints, floor)
	}
	return finite, floor, hasFloor, nil
}

// fitLinear is least squares of y on x (or on log10 x).
func fitLinear(pts []Point, base Law, logX bool) (Law, bool) {
	xs := make([]float64, len(pts))
	for i, p := range pts {
		xs[i] = p.X
		if logX {
			if p.X <= 0 {
				return Law{}, false
			}
			xs[i] = math.Log10(p.X)
		}
	}
	var sx, sy, sxx, sxy float64
	for i, p := range pts {
		sx, sy, sxx, sxy = sx+xs[i], sy+p.Y, sxx+xs[i]*xs[i], sxy+xs[i]*p.Y
	}
	n := float64(len(pts))
	den := n*sxx - sx*sx
	if den == 0 {
		return Law{}, false
	}
	l := base
	l.Kind, l.A = Linear, (n*sxy-sx*sy)/den
	l.B = (sy - l.A*sx) / n
	if logX {
		l.Kind = Log10
	}
	l.MaxResidual = maxResidual(l, pts)
	return l, true
}

// fitPiecewise joins the points with as few straight segments as the
// tolerance allows (greedy: extend a segment while it passes every point).
func fitPiecewise(pts []Point, base Law, tol float64) Law {
	l := base
	l.Kind = Piecewise
	for i := 0; i < len(pts)-1; {
		j := i + 1
		for j+1 < len(pts) && fits(pts[i], pts[j+1], pts[i+1:j+1], tol) {
			j++
		}
		l.Segments = append(l.Segments, through(pts[i], pts[j]))
		i = j
	}
	l.MaxResidual = maxResidual(l, pts)
	return l
}

func through(a, b Point) Segment {
	s := (b.Y - a.Y) / (b.X - a.X)
	return Segment{X0: a.X, X1: b.X, Slope: s, Intercept: a.Y - s*a.X}
}

func fits(a, b Point, inner []Point, tol float64) bool {
	seg := through(a, b)
	for _, p := range inner {
		if math.Abs(seg.Slope*p.X+seg.Intercept-p.Y) > tol {
			return false
		}
	}
	return true
}

func maxResidual(l Law, pts []Point) float64 {
	worst := 0.0
	for _, p := range pts {
		worst = math.Max(worst, math.Abs(l.Eval(p.X)-p.Y))
	}
	return worst
}

// Eval returns the law's y for x. Outside the fitted range a piecewise law
// extends its first or last segment.
func (l Law) Eval(x float64) float64 {
	if l.HasFloor && x <= l.FloorX {
		return math.Inf(-1)
	}
	switch l.Kind {
	case Linear:
		return l.A*x + l.B
	case Log10:
		if x <= 0 {
			return math.Inf(-1)
		}
		return l.A*math.Log10(x) + l.B
	}
	seg := l.Segments[len(l.Segments)-1]
	for _, s := range l.Segments {
		if x <= s.X1 {
			seg = s
			break
		}
	}
	return seg.Slope*x + seg.Intercept
}

// String renders the law for the operator and the report.
func (l Law) String() string {
	var b strings.Builder
	switch l.Kind {
	case Linear:
		fmt.Fprintf(&b, "linear: y = %s", line(l.A, "x", l.B))
	case Log10:
		fmt.Fprintf(&b, "log10: y = %s", line(l.A, "log10(x)", l.B))
	default:
		fmt.Fprintf(&b, "piecewise linear, %d segment(s):", len(l.Segments))
		for _, s := range l.Segments {
			fmt.Fprintf(&b, " [%.6g..%.6g] y = %s;", s.X0, s.X1, line(s.Slope, "x", s.Intercept))
		}
	}
	fmt.Fprintf(&b, " max residual %.3g over %d points", l.MaxResidual, l.Points)
	if l.HasFloor {
		fmt.Fprintf(&b, "; x <= %.6g is -inf", l.FloorX)
	}
	return b.String()
}

func line(a float64, x string, c float64) string {
	sign := "+"
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%.6g*%s %s %.6g", a, x, sign, c)
}
