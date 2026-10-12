// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// Fader law, docs/protocols/x32.md X32-F40..F43.
const (
	faderMaxDB = 10.0
	faderMinDB = -90.0
	// faderSteps: valid wire values are k/1023, k = 0..1023 (X32-F41).
	faderSteps = 1023.0
	// nameMaxLen is the longest channel name the console stores (X32-F31).
	nameMaxLen = 12
)

// errBadWire marks a value from the console that does not fit the documented
// encoding. Such a message is dropped, never guessed.
var errBadWire = errors.New("x32: value outside the documented encoding")

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
		return 0, fmt.Errorf("x32 fader NaN: %w", errBadWire)
	}
	return positionToDB(float64(f)), nil
}

// dbToFader converts dB to the wire float, clamped and rounded to the nearest
// console step (X32-F41).
func dbToFader(db float64) (float32, error) {
	if math.IsNaN(db) {
		return 0, fmt.Errorf("x32 fader: NaN dB: %w", model.ErrInvalidValue)
	}
	k := math.Round(dbToPosition(db) * faderSteps)
	return float32(k / faderSteps), nil
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

// switchToWire converts a model mute to an int switch: mix/on and dca/on are
// the inverse of mute (X32-F33, X32-F37), a mute group is engaged = 1 (X32-F35).
func switchToWire(c conv, muted bool) int32 {
	if muted == (c == convEngaged) {
		return 1
	}
	return 0
}

func switchFromWire(c conv, v int32) (bool, error) {
	if v != 0 && v != 1 {
		return false, fmt.Errorf("x32 switch %d: %w", v, errBadWire)
	}
	return (v == 1) == (c == convEngaged), nil
}

func maskFromWire(p param, v int32) (model.Value, error) {
	if v < 0 || v > p.maxMask {
		return model.Value{}, fmt.Errorf("x32 %s bitmap %d: %w", p.addr, v, errBadWire)
	}
	return model.Mask(uint32(v)), nil
}

// nameToWire accepts at most 12 printable ASCII characters (X32-F31).
func nameToWire(s string) (string, error) {
	if len(s) > nameMaxLen {
		return "", fmt.Errorf("x32 name longer than %d characters: %w", nameMaxLen, model.ErrInvalidValue)
	}
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return "", fmt.Errorf("x32 name: only printable ASCII is verified: %w", model.ErrInvalidValue)
		}
	}
	return s, nil
}

func nameFromWire(b []byte) string { return strings.ToValidUTF8(string(b), "�") }

// toWire converts an engineering value to the OSC argument for p.
func toWire(p param, v model.Value) (osc.Arg, error) {
	if v.Kind() != p.ref.Field.ValueKind() {
		return osc.Arg{}, fmt.Errorf("x32 %s: value kind %d: %w", p.addr, v.Kind(), model.ErrInvalidValue)
	}
	switch p.conv {
	case convLevel:
		db, _ := v.DB()
		f, err := dbToFader(db)
		return osc.Float(f), err
	case convOnSwitch, convEngaged:
		m, _ := v.Bool()
		return osc.Int(switchToWire(p.conv, m)), nil
	case convName:
		s, _ := v.Text()
		n, err := nameToWire(s)
		return osc.String(n), err
	default: // membership is read-only (Apply refuses it before)
		return osc.Arg{}, fmt.Errorf("x32 %s is read-only: %w", p.addr, model.ErrInvalidValue)
	}
}

// fromWire converts an OSC argument for p to an engineering value.
func fromWire(p param, a osc.Arg) (model.Value, error) {
	if a.Type != p.typ {
		return model.Value{}, fmt.Errorf("x32 %s: wire type %q: %w", p.addr, byte(a.Type), errBadWire)
	}
	switch p.conv {
	case convLevel:
		db, err := faderToDB(a.Float)
		if err != nil {
			return model.Value{}, err
		}
		return model.Level(db)
	case convOnSwitch, convEngaged:
		m, err := switchFromWire(p.conv, a.Int)
		return model.Bool(m), err
	case convMask:
		return maskFromWire(p, a.Int)
	default:
		return model.Text(nameFromWire(a.Bytes))
	}
}
