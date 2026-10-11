// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"fmt"
	"math"

	"github.com/mixproto/mixcore/internal/osc"
)

const (
	inputChannels = 32   // X32-F30
	nameMaxLen    = 12   // X32-F31
	levelSteps    = 1023 // X32-F41: valid level floats are k/1023
)

// value is one stored console parameter in its wire form.
type value struct {
	typ osc.Type
	f   float32
	i   int32
	s   string
}

// newParams builds the modelled parameter tree. Initial values are a
// simulator choice (not a protocol fact): faders at 0.0 (off), switches on,
// empty names.
func newParams() map[string]*value {
	p := map[string]*value{}
	for ch := 1; ch <= inputChannels; ch++ {
		p[fmt.Sprintf("/ch/%02d/config/name", ch)] = &value{typ: osc.TypeString} // X32-F31
		p[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = &value{typ: osc.TypeFloat}    // X32-F32
		p[fmt.Sprintf("/ch/%02d/mix/on", ch)] = &value{typ: osc.TypeInt, i: 1}   // X32-F33
	}
	p["/main/st/mix/fader"] = &value{typ: osc.TypeFloat}  // X32-F32, X32-F34
	p["/main/st/mix/on"] = &value{typ: osc.TypeInt, i: 1} // X32-F33, X32-F34
	return p
}

func (v *value) arg() osc.Arg {
	switch v.typ {
	case osc.TypeFloat:
		return osc.Float(v.f)
	case osc.TypeInt:
		return osc.Int(v.i)
	default:
		return osc.String(v.s)
	}
}

// set applies a wire argument the way the documentation describes. It returns
// false (value unchanged) for a wrong type or a value outside the documented
// encoding; the real console's reaction to those is [UNVERIFIED] (X32-F05).
func (v *value) set(a osc.Arg) bool {
	if a.Type != v.typ {
		return false
	}
	switch v.typ {
	case osc.TypeFloat:
		if math.IsNaN(float64(a.Float)) {
			return false
		}
		v.f = quantizeLevel(a.Float)
	case osc.TypeInt:
		if a.Int != 0 && a.Int != 1 { // X32-F33: 0 or 1
			return false
		}
		v.i = a.Int
	default:
		if len(a.Bytes) > nameMaxLen { // X32-F31
			return false
		}
		v.s = string(a.Bytes)
	}
	return true
}

// quantizeLevel clamps to 0..1 and rounds to the nearest of the 1024 known
// steps (X32-F41).
func quantizeLevel(f float32) float32 {
	x := math.Min(math.Max(float64(f), 0), 1)
	return float32(math.Round(x*levelSteps) / levelSteps)
}
