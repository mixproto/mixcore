// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// errBadWire marks a value from the console that does not fit the documented
// encoding of its parameter. Such a value is dropped, never guessed.
var errBadWire = errors.New("wing: value outside the documented encoding")

// wireKind is the kind of a value token (WG-F30).
type wireKind uint8

const (
	wireNone  wireKind = iota
	wireInt            // 00..3F, D3, D4
	wireFloat          // D5, in the parameter's unit
	wireRaw            // D6 raw 0..1: never used by mixcore (wing.md 2.1)
	wireText           // D0, 80..BF, D1
)

// wireValue is one value as it travels on the data channel.
type wireValue struct {
	kind wireKind
	i    int32
	f    float32
	s    string
}

// valueOf converts a value token; ok is false for any other token.
func valueOf(tok wingproto.Token) (wireValue, bool) {
	switch tok.Kind {
	case wingproto.KindInt:
		return wireValue{kind: wireInt, i: tok.Int}, true
	case wingproto.KindFloat:
		return wireValue{kind: wireFloat, f: tok.Float}, true
	case wingproto.KindRawFloat:
		return wireValue{kind: wireRaw, f: tok.Float}, true
	case wingproto.KindString:
		return wireValue{kind: wireText, s: string(tok.Bytes)}, true
	default:
		return wireValue{}, false
	}
}

// appendValue appends the value token of v (absolute values only, never a
// toggle or step: wing.md 2.4).
func appendValue(dst []byte, v wireValue) ([]byte, error) {
	switch v.kind {
	case wireInt:
		return wingproto.AppendInt(dst, v.i), nil
	case wireFloat:
		return wingproto.AppendFloat(dst, v.f), nil
	case wireText:
		return wingproto.AppendString(dst, v.s)
	default:
		return dst, fmt.Errorf("wing: no encoding for value kind %d: %w", v.kind, model.ErrInvalidValue)
	}
}

// switchFromWire reads a 0/1 int: mute 1 = muted (wing.md 3.2), a mute
// group 1 = muting, phantom 1 = on, a lock 1 = locked. Anything else is dropped.
func switchFromWire(v wireValue) (bool, error) {
	if v.kind != wireInt || (v.i != 0 && v.i != 1) {
		return false, fmt.Errorf("switch %+v: %w", v, errBadWire)
	}
	return v.i == 1, nil
}

func switchToWire(on bool) wireValue {
	if on {
		return wireValue{kind: wireInt, i: 1}
	}
	return wireValue{kind: wireInt}
}

// textFromWire accepts valid UTF-8 only.
func textFromWire(v wireValue) (string, error) {
	if v.kind != wireText || !utf8.ValidString(v.s) {
		return "", fmt.Errorf("text: %w", errBadWire)
	}
	return v.s, nil
}

// enumNameFromWire reads an enum value sent as an index or a name (WG-F65).
// An index is taken as 0-based into the definition's items (unverified:
// the console's choice of form is not documented); a name is looked up.
func enumNameFromWire(v wireValue, items []wingproto.EnumItem) (string, error) {
	switch {
	case v.kind == wireText:
		return v.s, nil
	case v.kind == wireInt && v.i >= 0 && int(v.i) < len(items):
		return items[v.i].Name, nil
	}
	return "", fmt.Errorf("enum %+v: %w", v, errBadWire)
}

// intFromWire reads an int within [lo, hi].
func intFromWire(v wireValue, lo, hi int32) (int32, error) {
	if v.kind != wireInt || v.i < lo || v.i > hi {
		return 0, fmt.Errorf("int %+v not in %d..%d: %w", v, lo, hi, errBadWire)
	}
	return v.i, nil
}
