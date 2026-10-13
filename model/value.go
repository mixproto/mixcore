// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package model is the console model shared by every driver and app: strips,
// parameters and values in engineering units (dB, on/off, names), immutable
// state snapshots and capabilities. It does no I/O and knows no protocol.
package model

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// Sentinel errors. Compare with errors.Is.
var (
	ErrInvalidValue = errors.New("model: invalid value")
	ErrUnknownParam = errors.New("model: unknown parameter")
	ErrInvalidState = errors.New("model: invalid state")
)

// ValueKind is the engineering-unit type of a parameter value.
type ValueKind uint8

// Value kinds.
const (
	KindLevel  ValueKind = iota + 1 // dB, finite or -inf (off)
	KindBool                        // on/off
	KindText                        // UTF-8 text
	KindMask                        // group membership: bit n-1 set = member of group n
	KindIndex                       // index of another strip, 0 = none (an input's headamp)
	KindPan                         // pan position -100 (left) .. +100 (right)
	KindTap                         // where a send taps the channel (Tap)
	KindColour                      // the console's strip colour (Colour)
)

// Value is an immutable engineering-unit value. The zero Value is invalid.
type Value struct {
	kind ValueKind
	db   float64 // a level in dB, or a pan position (KindPan)
	on   bool
	text string
	mask uint32
	n    int // an index, a Tap or a Colour
}

// Level returns a level in dB. -inf means fully off. NaN and +inf are invalid,
// so they can never reach a console.
func Level(db float64) (Value, error) {
	if math.IsNaN(db) || math.IsInf(db, 1) {
		return Value{}, fmt.Errorf("level %v dB: %w", db, ErrInvalidValue)
	}
	return Value{kind: KindLevel, db: db}, nil
}

// Bool returns an on/off value.
func Bool(on bool) Value { return Value{kind: KindBool, on: on} }

// Text returns a text value; s must be valid UTF-8.
func Text(s string) (Value, error) {
	if !utf8.ValidString(s) {
		return Value{}, fmt.Errorf("text is not UTF-8: %w", ErrInvalidValue)
	}
	return Value{kind: KindText, text: s}, nil
}

// Mask returns a group-membership value: bit n-1 set means member of group n.
func Mask(m uint32) Value { return Value{kind: KindMask, mask: m} }

// Index returns a strip index value; 0 means none. Negative is invalid.
func Index(n int) (Value, error) {
	if n < 0 {
		return Value{}, fmt.Errorf("index %d: %w", n, ErrInvalidValue)
	}
	return Value{kind: KindIndex, n: n}, nil
}

// Pan bounds: -100 is hard left, +100 hard right.
const (
	PanLeft  = -100.0
	PanRight = 100.0
)

// Pan returns a pan position. NaN and anything outside -100..+100 are
// invalid: a pan is never clamped into range on its way to a console.
func Pan(p float64) (Value, error) {
	if !(p >= PanLeft && p <= PanRight) {
		return Value{}, fmt.Errorf("pan %v outside %v..%v: %w", p, PanLeft, PanRight, ErrInvalidValue)
	}
	return Value{kind: KindPan, db: p}, nil
}

// TapValue returns a send tap value.
func TapValue(t Tap) (Value, error) {
	if !t.valid() {
		return Value{}, fmt.Errorf("tap %d: %w", t, ErrInvalidValue)
	}
	return Value{kind: KindTap, n: int(t)}, nil
}

// ColourValue returns a strip colour value.
func ColourValue(c Colour) (Value, error) {
	if c >= ColourCount {
		return Value{}, fmt.Errorf("colour %d: %w", c, ErrInvalidValue)
	}
	return Value{kind: KindColour, n: int(c)}, nil
}

// Kind returns the value kind (0 for the invalid zero Value).
func (v Value) Kind() ValueKind { return v.kind }

// DB returns the level in dB if v is a level.
func (v Value) DB() (float64, bool) { return v.db, v.kind == KindLevel }

// Bool returns the on/off state if v is a bool.
func (v Value) Bool() (bool, bool) { return v.on, v.kind == KindBool }

// Mask returns the membership bits if v is a mask.
func (v Value) Mask() (uint32, bool) { return v.mask, v.kind == KindMask }

// Index returns the strip index (0 = none) if v is an index.
func (v Value) Index() (int, bool) { return v.n, v.kind == KindIndex }

// Pan returns the pan position if v is a pan.
func (v Value) Pan() (float64, bool) { return v.db, v.kind == KindPan }

// Tap returns the send tap if v is a tap.
func (v Value) Tap() (Tap, bool) { return Tap(v.n), v.kind == KindTap }

// Colour returns the colour if v is a colour.
func (v Value) Colour() (Colour, bool) { return Colour(v.n), v.kind == KindColour }

// Text returns the text if v is text.
func (v Value) Text() (string, bool) { return v.text, v.kind == KindText }

// Equal reports exact equality (two -inf levels are equal).
func (v Value) Equal(o Value) bool { return v == o }

// String renders the value for logs.
func (v Value) String() string {
	switch v.kind {
	case KindLevel:
		return fmt.Sprintf("%.2f dB", v.db)
	case KindBool:
		return fmt.Sprintf("%t", v.on)
	case KindText:
		return fmt.Sprintf("%q", v.text)
	case KindMask:
		return fmt.Sprintf("mask %b", v.mask)
	case KindIndex:
		return fmt.Sprintf("index %d", v.n)
	case KindPan:
		return fmt.Sprintf("pan %.0f", v.db)
	case KindTap:
		return "tap " + Tap(v.n).String()
	case KindColour:
		return "colour " + Colour(v.n).String()
	default:
		return "<invalid>"
	}
}
