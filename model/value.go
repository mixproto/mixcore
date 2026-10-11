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
	KindLevel ValueKind = iota + 1 // dB, finite or -inf (off)
	KindBool                       // on/off
	KindText                       // UTF-8 text
)

// Value is an immutable engineering-unit value. The zero Value is invalid.
type Value struct {
	kind ValueKind
	db   float64
	on   bool
	text string
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

// Kind returns the value kind (0 for the invalid zero Value).
func (v Value) Kind() ValueKind { return v.kind }

// DB returns the level in dB if v is a level.
func (v Value) DB() (float64, bool) { return v.db, v.kind == KindLevel }

// Bool returns the on/off state if v is a bool.
func (v Value) Bool() (bool, bool) { return v.on, v.kind == KindBool }

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
	default:
		return "<invalid>"
	}
}
