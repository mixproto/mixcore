// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package oscsim

import (
	"math"

	"github.com/mixproto/mixcore/internal/osc"
)

// Value is one stored console parameter in its wire form. Families build
// their parameter tree from the constructors below, each citing the protocol
// fact that defines the range.
type Value struct {
	typ        osc.Type
	f          float32
	steps      float64 // float: valid values are k/steps; 0 keeps any float in 0..1
	i          int32
	minI, maxI int32 // valid range of an int parameter
	s          string
	maxLen     int // longest accepted string
}

// Level is a 0..1 float rounded to the nearest of steps+1 known values
// (fader law steps, e.g. 1023 for faders, X32-F41, XA-F41).
func Level(steps int) *Value { return &Value{typ: osc.TypeFloat, steps: float64(steps)} }

// LevelAt is Level with an initial value (stored as given).
func LevelAt(steps int, f float32) *Value {
	v := Level(steps)
	v.f = f
	return v
}

// Switch is an int 0..1 with an initial value (mix/on, mute group engaged).
func Switch(on int32) *Value { return &Value{typ: osc.TypeInt, i: on, maxI: 1} }

// IntRange is an int in min..max with an initial value (bitmaps, indexes).
func IntRange(minI, maxI, init int32) *Value {
	return &Value{typ: osc.TypeInt, i: init, minI: minI, maxI: maxI}
}

// Text is a string of at most maxLen bytes, initially empty.
func Text(maxLen int) *Value { return &Value{typ: osc.TypeString, maxLen: maxLen} }

// Arg returns the stored value as an OSC argument.
func (v *Value) Arg() osc.Arg {
	switch v.typ {
	case osc.TypeFloat:
		return osc.Float(v.f)
	case osc.TypeInt:
		return osc.Int(v.i)
	default:
		return osc.String(v.s)
	}
}

// SetInt stores i without checks (the console itself changing a marker).
func (v *Value) SetInt(i int32) { v.i = i }

// SetString stores s without checks (the console itself changing a marker).
func (v *Value) SetString(s string) { v.s = s }

// Set applies a wire argument the way the protocol documents describe. It
// returns false (value unchanged) for a wrong type or a value outside the
// documented encoding; how a real console reacts to those is unverified
// (X32-F05, XA-F03).
func (v *Value) Set(a osc.Arg) bool {
	if a.Type != v.typ {
		return false
	}
	switch v.typ {
	case osc.TypeFloat:
		if math.IsNaN(float64(a.Float)) {
			return false
		}
		v.f = quantize(a.Float, v.steps)
	case osc.TypeInt:
		if a.Int < v.minI || a.Int > v.maxI {
			return false
		}
		v.i = a.Int
	default:
		if len(a.Bytes) > v.maxLen {
			return false
		}
		v.s = string(a.Bytes)
	}
	return true
}

// quantize clamps to 0..1 and rounds to the nearest of the known steps.
func quantize(f float32, steps float64) float32 {
	x := math.Min(math.Max(float64(f), 0), 1)
	if steps == 0 {
		return float32(x)
	}
	return float32(math.Round(x*steps) / steps)
}
