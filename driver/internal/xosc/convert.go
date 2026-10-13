// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// Fader law, docs/protocols/x32.md X32-F40..F43; the same law and steps on
// X Air (XA-F40, XA-F41).
const (
	faderMaxDB = 10.0
	faderMinDB = -90.0
	// faderSteps: valid wire values are k/1023, k = 0..1023 (X32-F41).
	faderSteps = 1023.0
	// nameMaxLen is the longest channel name the console stores (X32-F31);
	// X Air keeps the X32 rule until hardware test item 14 (XA section 3.2).
	nameMaxLen = 12
)

// errBadWire marks a value from the console that does not fit the documented
// encoding. Such a message is dropped, never guessed.
var errBadWire = errors.New("value outside the documented encoding")

// Segments of the X32 level law: position p in [lo, hi) maps to dB = slope*p + offset.
var faderSegments = [...]struct{ lo, slope, offset float64 }{
	{0.5, 40, -30},
	{0.25, 80, -50},
	{0.0625, 160, -70},
	{0, 480, -90},
}

// positionToDB is the continuous law; p <= 0 is -inf (X32-F42), p >= 1 is +10 dB.
func positionToDB(p float64) float64 {
	switch {
	case !(p > 0):
		return math.Inf(-1)
	case p >= 1:
		return faderMaxDB
	}
	for _, s := range faderSegments {
		if p >= s.lo {
			return p*s.slope + s.offset
		}
	}
	return math.Inf(-1) // unreachable: the last segment starts at 0
}

// stepToDB is the dB of step k of a grid of steps, computed as
// slope*k/steps + offset so that a step the console prints as a round
// value (X32-F83 table: k 128 = +2.0 dB) comes out exactly.
func stepToDB(k, steps float64) float64 {
	switch {
	case k <= 0:
		return math.Inf(-1)
	case k >= steps:
		return faderMaxDB
	}
	for _, s := range faderSegments {
		if k/steps >= s.lo {
			return s.slope*k/steps + s.offset
		}
	}
	return math.Inf(-1) // unreachable: the last segment starts at 0
}

// dbToPosition is the continuous inverse; NaN and anything at or below -90 dB give 0.
func dbToPosition(db float64) float64 {
	switch {
	case math.IsNaN(db) || db <= faderMinDB:
		return 0
	case db >= faderMaxDB:
		return 1
	}
	for _, s := range faderSegments {
		if p := (db - s.offset) / s.slope; p >= s.lo {
			return p
		}
	}
	return 0 // unreachable: db > -90 always lands in the last segment
}

// faderToDB converts a wire float from the console to dB.
func faderToDB(f float32) (float64, error) {
	if math.IsNaN(float64(f)) {
		return 0, fmt.Errorf("fader NaN: %w", errBadWire)
	}
	return positionToDB(float64(f)), nil
}

// dbToFader converts dB to the wire float, clamped and rounded to the nearest
// console step (X32-F41).
func dbToFader(db float64) (float32, error) { return dbToLevel(db, faderSteps) }

// dbToLevel converts dB to a level-law wire float on a grid of steps:
// 1023 for faders (X32-F41), 160 for send levels (X32-F83, XA-F41).
func dbToLevel(db, steps float64) (float32, error) {
	if math.IsNaN(db) {
		return 0, fmt.Errorf("level: NaN dB: %w", model.ErrInvalidValue)
	}
	k := math.Round(dbToPosition(db) * steps)
	return float32(k / steps), nil
}

// panToWire converts a pan (-100..+100, model-validated) to the wire float
// on the console's step of 2 (X32-F84, xair.md section 4): f = k/100.
func panToWire(pan float64) float32 {
	k := math.Round((pan - model.PanLeft) / panStepWidth)
	return float32(k / panWireSteps)
}

// panFromWire converts a wire float to a pan on the step of 2.
func panFromWire(f float32) (model.Value, error) {
	if !(f >= 0 && f <= 1) {
		return model.Value{}, fmt.Errorf("pan wire %v: %w", f, errBadWire)
	}
	k := math.Round(float64(f) * panWireSteps)
	return model.Pan(model.PanLeft + k*panStepWidth)
}

// panStepWidth is the pan step in model units (X32-F84: linf step 2.000).
const panStepWidth = 2.0

// tapFromWire converts the tap int 0..5 to a model tap (X32-F85, xair.md 3.3).
func tapFromWire(v int32) (model.Value, error) {
	if v < 0 || v >= int32(model.TapCount) {
		return model.Value{}, fmt.Errorf("tap %d: %w", v, errBadWire)
	}
	return model.TapValue(model.Tap(v) + model.TapInput)
}

// colourFromWire converts config/color 0..15 (X32-F89, xair.md 3.2).
func colourFromWire(v int32) (model.Value, error) {
	if v < 0 || v > colourMax {
		return model.Value{}, fmt.Errorf("colour %d: %w", v, errBadWire)
	}
	return model.ColourValue(model.Colour(v))
}

// faderLaw is the UI-facing law: continuous, never blocks, never fails.
type faderLaw struct{}

func (faderLaw) PositionFromDB(db float64) float64 { return dbToPosition(db) }

func (faderLaw) DBFromPosition(pos float64) float64 {
	if math.IsNaN(pos) {
		return math.Inf(-1)
	}
	return positionToDB(min(pos, 1))
}

// switchToWire converts a model bool to an int switch: mix/on and dca/on are
// the inverse of mute (X32-F33, X32-F37); a mute group is engaged = 1
// (X32-F35) and phantom is on = 1 (X32-F73, XA-F113).
func switchToWire(c conv, on bool) int32 {
	if on == wireOneIsTrue(c) {
		return 1
	}
	return 0
}

func switchFromWire(c conv, v int32) (bool, error) {
	if v != 0 && v != 1 {
		return false, fmt.Errorf("switch %d: %w", v, errBadWire)
	}
	return (v == 1) == wireOneIsTrue(c), nil
}

func wireOneIsTrue(c conv) bool { return c == convEngaged || c == convFlag }

// haIndexFromWire converts /-ha/NN/index (X32-F74): -1 is no headamp
// (model 0), otherwise the headamp's wire number.
func haIndexFromWire(p param, v int32) (model.Value, error) {
	if v == -1 {
		return model.Index(0)
	}
	n := int(v) - p.haOffset
	if n < 1 || n > p.haMax {
		return model.Value{}, fmt.Errorf("%s headamp %d: %w", p.addr, v, errBadWire)
	}
	return model.Index(n)
}

func maskFromWire(p param, v int32) (model.Value, error) {
	if v < 0 || v > p.maxMask {
		return model.Value{}, fmt.Errorf("%s bitmap %d: %w", p.addr, v, errBadWire)
	}
	return model.Mask(uint32(v)), nil
}

// nameToWire accepts at most 12 printable ASCII characters (X32-F31).
func nameToWire(s string) (string, error) {
	if len(s) > nameMaxLen {
		return "", fmt.Errorf("name longer than %d characters: %w", nameMaxLen, model.ErrInvalidValue)
	}
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return "", fmt.Errorf("name: only printable ASCII is verified: %w", model.ErrInvalidValue)
		}
	}
	return s, nil
}

func nameFromWire(b []byte) string { return strings.ToValidUTF8(string(b), "�") }

// toWire converts an engineering value to the OSC argument for p.
func toWire(p param, v model.Value) (osc.Arg, error) {
	if v.Kind() != p.ref.Field.ValueKind() {
		return osc.Arg{}, fmt.Errorf("%s: value kind %d: %w", p.addr, v.Kind(), model.ErrInvalidValue)
	}
	switch p.conv {
	case convLevel:
		db, _ := v.DB()
		f, err := dbToLevel(db, p.wireSteps())
		return osc.Float(f), err
	case convPan:
		pan, _ := v.Pan()
		return osc.Float(panToWire(pan)), nil
	case convOnSwitch, convEngaged, convFlag:
		m, _ := v.Bool()
		return osc.Int(switchToWire(p.conv, m)), nil
	case convName:
		s, _ := v.Text()
		n, err := nameToWire(s)
		return osc.String(n), err
	case convGain:
		db, _ := v.DB()
		f, err := gainToWire(p, db)
		return osc.Float(f), err
	default: // membership, /-ha, tap and colour are read-only (Apply refuses them before)
		return osc.Arg{}, fmt.Errorf("%s is read-only: %w", p.addr, model.ErrInvalidValue)
	}
}

// fromWire converts an OSC argument for p to an engineering value.
func fromWire(p param, a osc.Arg) (model.Value, error) {
	if a.Type != p.typ {
		return model.Value{}, fmt.Errorf("%s: wire type %q: %w", p.addr, byte(a.Type), errBadWire)
	}
	switch p.conv {
	case convLevel:
		db, err := faderToDB(a.Float)
		if err != nil {
			return model.Value{}, err
		}
		if p.levelSteps > 0 { // a send: the console keeps only k/160 (X32-F83, XA-F41); report that step's own dB
			db = stepToDB(math.Round(math.Min(math.Max(float64(a.Float), 0), 1)*p.levelSteps), p.levelSteps)
		}
		return model.Level(db)
	case convOnSwitch, convEngaged, convFlag:
		m, err := switchFromWire(p.conv, a.Int)
		return model.Bool(m), err
	case convMask:
		return maskFromWire(p, a.Int)
	case convHaIndex:
		return haIndexFromWire(p, a.Int)
	case convPan:
		return panFromWire(a.Float)
	case convTap:
		return tapFromWire(a.Int)
	case convColour:
		return colourFromWire(a.Int)
	case convGain:
		db, err := gainFromWire(p, a.Float)
		if err != nil {
			return model.Value{}, err
		}
		return model.Level(db)
	default:
		return model.Text(nameFromWire(a.Bytes))
	}
}

// gainToWire converts Gain in dB to the wire float k/steps (X32-F71,
// XA-F111, XA-F112). A value outside the range is an error, never clamped
// (phone-safety.md 5.1: "Out of range: error, no clamp"). It rounds to the
// nearest console step below the top. The wire maximum 1.0 is never
// written: XS10 avoids it ("strange bug when setting 1") and whether it is
// safe is unverified (XA-F116 item 22, X32-F72 item 17); it is also exactly
// what a jump to the maximum would send (re-review LOW D). So the top of
// the law (+60 mic, +20 line) is refused; reading 1.0 is fine.
func gainToWire(p param, db float64) (float32, error) {
	top := p.gainMin + float64(p.gainSteps)*p.gainStep
	if math.IsNaN(db) || db < p.gainMin || db > top {
		return 0, fmt.Errorf("gain %v dB outside %v..%v: %w", db, p.gainMin, top, model.ErrInvalidValue)
	}
	k := min(int(math.Round((db-p.gainMin)/p.gainStep)), p.gainSteps-1)
	if db >= top {
		return 0, fmt.Errorf("gain %v dB would write the wire maximum, unverified (XA-F116, X32-F72): %w", db, model.ErrInvalidValue)
	}
	return float32(float64(k) / float64(p.gainSteps)), nil
}

// gainFromWire converts a wire float to Gain in dB on the console's step.
func gainFromWire(p param, f float32) (float64, error) {
	if math.IsNaN(float64(f)) || f < 0 || f > 1 {
		return 0, fmt.Errorf("gain wire %v: %w", f, errBadWire)
	}
	k := math.Round(float64(f) * float64(p.gainSteps))
	return p.gainMin + k*p.gainStep, nil
}
