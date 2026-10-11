// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package osc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const (
	align        = 4
	bundlePrefix = "#bundle"
	tagPrefix    = ','
)

func padLen(n int) int { return (align - n%align) % align }

// Append encodes m to dst and returns the extended buffer. On error it
// returns nil and nothing partial escapes.
func Append(dst []byte, m *Message) ([]byte, error) {
	if err := checkAddress(string(m.Address)); err != nil {
		return nil, err
	}
	if !m.Tagless {
		if err := checkArgs(m.Args()); err != nil {
			return nil, err
		}
		dst = appendPadded(append(dst, m.Address...), len(m.Address))
		return appendTagsAndArgs(dst, m.Args()), nil
	}
	if len(m.Untagged)%align != 0 {
		return nil, fmt.Errorf("osc: untagged payload of %d bytes: %w", len(m.Untagged), ErrTruncated)
	}
	dst = appendPadded(append(dst, m.Address...), len(m.Address))
	return append(dst, m.Untagged...), nil
}

// AppendString encodes a message with a string address. It does not allocate
// when dst has enough capacity.
func AppendString(dst []byte, address string, args ...Arg) ([]byte, error) {
	if err := checkAddress(address); err != nil {
		return nil, err
	}
	if err := checkArgs(args); err != nil {
		return nil, err
	}
	dst = appendPadded(append(dst, address...), len(address))
	return appendTagsAndArgs(dst, args), nil
}

func checkArgs(args []Arg) error {
	if len(args) > MaxArgs {
		return ErrTooManyArgs
	}
	for _, a := range args {
		if err := checkArg(a); err != nil {
			return err
		}
	}
	return nil
}

func appendTagsAndArgs(dst []byte, args []Arg) []byte {
	dst = append(dst, tagPrefix)
	for _, a := range args {
		dst = append(dst, byte(a.Type))
	}
	dst = appendPadded(dst, len(args)+1)
	for _, a := range args {
		dst = appendArg(dst, a)
	}
	return dst
}

func checkAddress(a string) error {
	if len(a) == 0 || a[0] != '/' || strings.IndexByte(a, 0) >= 0 {
		return ErrBadAddress
	}
	return nil
}

func checkArg(a Arg) error {
	switch a.Type {
	case TypeInt, TypeFloat, TypeBlob:
		return nil
	case TypeString:
		if bytes.IndexByte(a.Bytes, 0) >= 0 {
			return ErrBadString
		}
		return nil
	default:
		return ErrUnsupportedType
	}
}

// appendPadded appends the NUL terminator and padding after n bytes of string data.
func appendPadded(dst []byte, n int) []byte {
	dst = append(dst, 0)
	for range padLen(n + 1) {
		dst = append(dst, 0)
	}
	return dst
}

func appendArg(dst []byte, a Arg) []byte {
	switch a.Type {
	case TypeInt:
		return binary.BigEndian.AppendUint32(dst, uint32(a.Int))
	case TypeFloat:
		return binary.BigEndian.AppendUint32(dst, math.Float32bits(a.Float))
	case TypeString:
		return appendPadded(append(dst, a.Bytes...), len(a.Bytes))
	default: // TypeBlob, validated by checkArg
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(a.Bytes)))
		dst = append(dst, a.Bytes...)
		for range padLen(len(a.Bytes)) {
			dst = append(dst, 0)
		}
		return dst
	}
}

// Decode parses one OSC message from b into m without copying or allocating.
// m aliases b afterwards. On error m is left reset.
func Decode(b []byte, m *Message) error {
	m.Reset()
	if err := decodeInto(b, m); err != nil {
		m.Reset()
		return err
	}
	return nil
}

func decodeInto(b []byte, m *Message) error {
	if len(b) == 0 {
		return ErrTruncated
	}
	if bytes.HasPrefix(b, []byte(bundlePrefix)) {
		return ErrBundle
	}
	if b[0] != '/' {
		return ErrBadAddress
	}
	addr, rest, err := readPaddedString(b)
	if err != nil {
		return err
	}
	m.Address = addr
	if len(rest) == 0 || rest[0] != tagPrefix {
		if len(rest)%align != 0 {
			return ErrTruncated
		}
		m.Tagless = true
		if len(rest) > 0 {
			m.Untagged = rest
		}
		return nil
	}
	tags, rest, err := readPaddedString(rest)
	if err != nil {
		return err
	}
	for _, t := range tags[1:] {
		a, next, err := readArg(Type(t), rest)
		if err != nil {
			return err
		}
		if !m.appendArg(a) {
			return ErrTooManyArgs
		}
		rest = next
	}
	if len(rest) != 0 {
		return ErrTrailingData
	}
	return nil
}

// readPaddedString returns the string before the first NUL and the remainder
// after its 4-byte padding.
func readPaddedString(b []byte) (s, rest []byte, err error) {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		return nil, nil, ErrTruncated
	}
	end := n + 1 + padLen(n+1)
	if end > len(b) {
		return nil, nil, ErrTruncated
	}
	return b[:n], b[end:], nil
}

func readArg(t Type, b []byte) (Arg, []byte, error) {
	switch t {
	case TypeInt, TypeFloat:
		if len(b) < align {
			return Arg{}, nil, ErrTruncated
		}
		v := binary.BigEndian.Uint32(b)
		if t == TypeInt {
			return Int(int32(v)), b[align:], nil
		}
		return Float(math.Float32frombits(v)), b[align:], nil
	case TypeString:
		s, rest, err := readPaddedString(b)
		if err != nil {
			return Arg{}, nil, err
		}
		return StringBytes(s), rest, nil
	case TypeBlob:
		return readBlob(b)
	default:
		return Arg{}, nil, fmt.Errorf("osc: type %q: %w", byte(t), ErrUnsupportedType)
	}
}

func readBlob(b []byte) (Arg, []byte, error) {
	if len(b) < align {
		return Arg{}, nil, ErrTruncated
	}
	size := int64(int32(binary.BigEndian.Uint32(b)))
	b = b[align:]
	if size < 0 || size > int64(len(b)) {
		return Arg{}, nil, ErrTruncated
	}
	n := int(size)
	end := n + padLen(n)
	if end > len(b) {
		return Arg{}, nil, ErrTruncated
	}
	return Blob(b[:n]), b[end:], nil
}
