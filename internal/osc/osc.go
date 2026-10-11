// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package osc is a small OSC 1.0 message codec for console protocols.
//
// Encoding appends to a caller-owned buffer. Decoding never copies: the
// address, strings and blobs of a decoded Message are sub-slices of the input
// buffer and stay valid only while that buffer is not reused. Bundles are
// rejected; messages without a type tag string (older OSC form, which the
// OSC 1.0 spec asks receivers to tolerate) are accepted and their payload is
// kept raw in Message.Untagged.
//
// Spec: https://opensoundcontrol.stanford.edu/spec-1_0.html
package osc

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MaxArgs is the largest number of arguments a Message can hold. A packet with
// more arguments is rejected instead of being truncated.
const MaxArgs = 32

// Type is an OSC 1.0 type tag character.
type Type byte

// The four OSC 1.0 atomic types. Other tags are rejected.
const (
	TypeInt    Type = 'i'
	TypeFloat  Type = 'f'
	TypeString Type = 's'
	TypeBlob   Type = 'b'
)

// Sentinel errors. Compare with errors.Is.
var (
	ErrTruncated       = errors.New("osc: truncated or misaligned packet")
	ErrBadAddress      = errors.New("osc: invalid address pattern")
	ErrBadString       = errors.New("osc: string contains NUL")
	ErrBundle          = errors.New("osc: bundles are not supported")
	ErrUnsupportedType = errors.New("osc: unsupported type tag")
	ErrTooManyArgs     = errors.New("osc: too many arguments")
	ErrTrailingData    = errors.New("osc: trailing data after arguments")
)

// Arg is one OSC argument. Bytes holds the string (without NUL) or blob data.
type Arg struct {
	Type  Type
	Int   int32
	Float float32
	Bytes []byte
}

// Int returns an int32 argument.
func Int(v int32) Arg { return Arg{Type: TypeInt, Int: v} }

// Float returns a float32 argument.
func Float(v float32) Arg { return Arg{Type: TypeFloat, Float: v} }

// String returns a string argument. It allocates; hot paths use StringBytes.
func String(s string) Arg { return Arg{Type: TypeString, Bytes: []byte(s)} }

// StringBytes returns a string argument that references b without copying.
func StringBytes(b []byte) Arg { return Arg{Type: TypeString, Bytes: b} }

// Blob returns a blob argument that references b without copying.
func Blob(b []byte) Arg { return Arg{Type: TypeBlob, Bytes: b} }

// Equal reports whether two arguments are identical. Floats compare by bit
// pattern so that NaN payloads survive a round trip check.
func (a Arg) Equal(b Arg) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case TypeInt:
		return a.Int == b.Int
	case TypeFloat:
		return math.Float32bits(a.Float) == math.Float32bits(b.Float)
	default:
		return string(a.Bytes) == string(b.Bytes)
	}
}

// Message is one decoded or to-be-encoded OSC message.
type Message struct {
	Address []byte
	// Tagless is true when the packet had no type tag string at all.
	Tagless bool
	// Untagged is the raw payload of a tagless message (may be empty).
	Untagged []byte
	args     [MaxArgs]Arg
	nargs    int
}

// Reset clears the message so it can be reused without allocating.
func (m *Message) Reset() {
	m.Address = nil
	m.Tagless = false
	m.Untagged = nil
	m.nargs = 0
}

// NumArgs returns the number of decoded arguments.
func (m *Message) NumArgs() int { return m.nargs }

// Arg returns argument i; ok is false when i is out of range.
func (m *Message) Arg(i int) (Arg, bool) {
	if i < 0 || i >= m.nargs {
		return Arg{}, false
	}
	return m.args[i], true
}

// Args returns the arguments as a slice that aliases the message.
func (m *Message) Args() []Arg { return m.args[:m.nargs] }

func (m *Message) appendArg(a Arg) bool {
	if m.nargs >= MaxArgs {
		return false
	}
	m.args[m.nargs] = a
	m.nargs++
	return true
}

// AddressIs reports whether the address equals s, without allocating.
func (m *Message) AddressIs(s string) bool { return string(m.Address) == s }

// FloatArg returns argument i if it is a float.
func (m *Message) FloatArg(i int) (float32, bool) {
	a, ok := m.Arg(i)
	if !ok || a.Type != TypeFloat {
		return 0, false
	}
	return a.Float, true
}

// IntArg returns argument i if it is an int.
func (m *Message) IntArg(i int) (int32, bool) {
	a, ok := m.Arg(i)
	if !ok || a.Type != TypeInt {
		return 0, false
	}
	return a.Int, true
}

// StringArg returns argument i if it is a string. It allocates the result.
func (m *Message) StringArg(i int) (string, bool) {
	a, ok := m.Arg(i)
	if !ok || a.Type != TypeString {
		return "", false
	}
	return string(a.Bytes), true
}

// BlobArg returns argument i if it is a blob, aliasing the decoded buffer.
func (m *Message) BlobArg(i int) ([]byte, bool) {
	a, ok := m.Arg(i)
	if !ok || a.Type != TypeBlob {
		return nil, false
	}
	return a.Bytes, true
}

// Equal reports whether two messages carry the same address, form and arguments.
func (m *Message) Equal(o *Message) bool {
	if string(m.Address) != string(o.Address) || m.Tagless != o.Tagless ||
		string(m.Untagged) != string(o.Untagged) || m.nargs != o.nargs {
		return false
	}
	for i := range m.nargs {
		if !m.args[i].Equal(o.args[i]) {
			return false
		}
	}
	return true
}

// String renders the message for logs and test failures (not for hot paths).
func (m *Message) String() string {
	var sb strings.Builder
	sb.Write(m.Address)
	if m.Tagless {
		if len(m.Untagged) > 0 {
			fmt.Fprintf(&sb, " <untagged %d bytes>", len(m.Untagged))
		}
		return sb.String()
	}
	sb.WriteString(" ,")
	for _, a := range m.Args() {
		sb.WriteByte(byte(a.Type))
	}
	for _, a := range m.Args() {
		sb.WriteByte(' ')
		switch a.Type {
		case TypeInt:
			sb.WriteString(strconv.FormatInt(int64(a.Int), 10))
		case TypeFloat:
			sb.WriteString(strconv.FormatFloat(float64(a.Float), 'g', -1, 32))
		case TypeString:
			sb.WriteString(strconv.Quote(string(a.Bytes)))
		default:
			fmt.Fprintf(&sb, "<blob %d bytes>", len(a.Bytes))
		}
	}
	return sb.String()
}
