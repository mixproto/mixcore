// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"fmt"
	"math"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// Fader facts (docs/protocols/wing.md WG-F60, WG-F61).
const (
	// faderWireMinDB is the console's "-oo": any value at or below it is
	// -inf, and -inf is written as it (WG-F60).
	faderWireMinDB = -144.0
	// faderMaxDB is the top of every fader and send level (WG-F60).
	faderMaxDB = 10.0
	// faderSteps: "-oo..10 in 1024 steps" (WG-F60). The console's rounding of
	// dB writes is unverified (item 10), so the steps are used only for the
	// echo tolerance: half a step of the display law below.
	faderSteps = 1023.0
	// faderDisplayMinDB is where the display law reaches position 0 above
	// -inf (the X32 four-segment law, below).
	faderDisplayMinDB = -90.0
)

// Display law of the fader (FaderLaw): the four-segment X32 law, whose top
// segment dB = 40 x raw - 30 matches every raw point WG-F61 publishes
// (-3 dB = 0.675, 0 dB = 0.750, +3 dB = 0.825). Below -3 dB the console's
// raw law is unverified (item 9). WING writes and reads dB on the wire
// (WG-F60), so this law only draws the scale and shapes gestures; it never
// reaches the console.
var faderSegments = [...]struct{ lo, slope, offset float64 }{
	{0.5, 40, -30},
	{0.25, 80, -50},
	{0.0625, 160, -70},
	{0, 480, -90},
}

// faderLaw implements driver.FaderLaw for the display scale.
type faderLaw struct{}

// PositionFromDB maps dB to 0..1; -inf, NaN and anything at or below -90 dB give 0.
func (faderLaw) PositionFromDB(db float64) float64 {
	switch {
	case math.IsNaN(db) || db <= faderDisplayMinDB:
		return 0
	case db >= faderMaxDB:
		return 1
	}
	for _, s := range faderSegments {
		if p := (db - s.offset) / s.slope; p >= s.lo {
			return p
		}
	}
	return 0
}

// DBFromPosition maps 0..1 to dB; 0 (and NaN) is -inf, 1 and above +10 dB.
func (faderLaw) DBFromPosition(pos float64) float64 {
	switch {
	case !(pos > 0):
		return math.Inf(-1)
	case pos >= 1:
		return faderMaxDB
	}
	for _, s := range faderSegments {
		if pos >= s.lo {
			return pos*s.slope + s.offset
		}
	}
	return math.Inf(-1)
}

// faderFromWire converts a fader float (dB, WG-F60) to the model: at or
// below -144 dB is -inf; NaN, +-Inf and values above +10 dB are outside the
// documented encoding and dropped, never guessed.
func faderFromWire(f float32) (float64, error) {
	v := float64(f)
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0) || v > faderMaxDB:
		return 0, fmt.Errorf("fader %v dB: %w", v, errBadWire)
	case v <= faderWireMinDB:
		return math.Inf(-1), nil
	}
	return v, nil
}

// faderToWire converts model dB to the wire: -inf and anything at or below
// -144 dB is -144.0, the console's -oo (WG-F60); values are clamped to +10
// dB; NaN is an error.
func faderToWire(db float64) (float32, error) {
	switch {
	case math.IsNaN(db):
		return 0, fmt.Errorf("fader NaN dB: %w", model.ErrInvalidValue)
	case db <= faderWireMinDB:
		return faderWireMinDB, nil
	case db >= faderMaxDB:
		return faderMaxDB, nil
	}
	return float32(db), nil
}

// sameFader: two fader values are one console value when their display
// positions are within half of one of the 1024 steps (WG-F60, wing.md 2.4:
// half a step of the parameter's law, never float equality).
func sameFader(a, b float64) bool {
	law := faderLaw{}
	return math.Abs(law.PositionFromDB(a)-law.PositionFromDB(b)) <= 0.5/faderSteps
}

// gainLaw is a headamp's linear law from the console's own definition
// (WG-F62, WG-F63): value = min + (max - min) k / (steps - 1).
type gainLaw struct {
	min, max, step float64
}

// minLawSteps: a linear law needs at least two points.
const minLawSteps = 2

// gainLawOf reads the law of a gain definition; ok is false for anything but
// a sane linear float in dB (then the headamp's Gain is read-only).
func gainLawOf(d wingproto.Def) (gainLaw, bool) {
	lo, hi := float64(d.Min), float64(d.Max)
	if d.Type != wingproto.TypeLinFloat || d.Steps < minLawSteps || !(hi > lo) || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return gainLaw{}, false
	}
	return gainLaw{min: lo, max: hi, step: (hi - lo) / float64(d.Steps-1)}, true
}

// writeMax is one step below the wire maximum: the maximum is never written
// until hardware item 14 shows it is safe (wing.md section 4).
func (l gainLaw) writeMax() float64 { return l.max - l.step }

// halfStep is the tolerance two values of this law are equal within.
func (l gainLaw) halfStep() float64 { return l.step / 2 }

// toWire snaps db to the law's grid (the driver always sends a grid value,
// wing.md section 4). Values below the minimum are clamped to it; values
// above writeMax are refused (contracts/core-api state.schema.json writeMaxDB).
func (l gainLaw) toWire(db float64) (float32, error) {
	switch {
	case math.IsNaN(db) || math.IsInf(db, 0):
		return 0, fmt.Errorf("gain %v dB: %w", db, model.ErrInvalidValue)
	case db > l.writeMax()+l.halfStep():
		return 0, fmt.Errorf("gain %.2f dB above the writable maximum %.2f dB: %w", db, l.writeMax(), model.ErrInvalidValue)
	case db <= l.min:
		return float32(l.min), nil
	}
	k := math.Round((db - l.min) / l.step)
	return float32(min(l.min+k*l.step, l.writeMax())), nil
}

// gainFromWire accepts a finite dB value within half a step of the range.
func (l gainLaw) gainFromWire(f float32) (float64, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) || v < l.min-l.halfStep() || v > l.max+l.halfStep() {
		return 0, fmt.Errorf("gain %v dB: %w", v, errBadWire)
	}
	return v, nil
}

// DCA tags: "#D1".."#D16" are reserved for DCA 1..16 assignment (wing.md 3.2).
const (
	dcaTagPrefix = "#D"
	maxDCAs      = 16
)

// dcaMaskFromTags reads the DCA membership from a tags string. The separator
// between tags is unverified (item 11), so any character that is not a digit
// ends a tag; "#Drums" or "#D17" are not DCA tags.
func dcaMaskFromTags(tags string) uint32 {
	var mask uint32
	for i := 0; i+len(dcaTagPrefix) < len(tags); i++ {
		if tags[i:i+len(dcaTagPrefix)] != dcaTagPrefix {
			continue
		}
		n, j := 0, i+len(dcaTagPrefix)
		for ; j < len(tags) && tags[j] >= '0' && tags[j] <= '9' && n <= maxDCAs; j++ {
			n = n*10 + int(tags[j]-'0')
		}
		if j > i+len(dcaTagPrefix) && n >= 1 && n <= maxDCAs && (j == len(tags) || tags[j] < '0' || tags[j] > '9') {
			mask |= 1 << (n - 1)
		}
	}
	return mask
}
