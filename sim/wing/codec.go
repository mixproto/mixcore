// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"encoding/binary"
	"math"
)

// The simulator's own codec of the native protocol, written from
// docs/protocols/wing.md WG-F13 (mux) and WG-F30 (tokens) without the
// driver's codec, so that one bug cannot hide in both.

const (
	esc       = 0xDF // WG-F13
	chanFirst = 0xD0 // DF D0..DD select channel 0..13
	chanLast  = 0xDD
	escDF     = 0xDE // DF DE = literal DF
	chanData  = 1    // DF D1 (WG-F14)
	chanMeter = 3    // DF D3 (WG-F14, WG-F50)
)

// rxMux is the receive side of the mux for one connection.
type rxMux struct {
	ch      int // -1 until the client selects one; treated as the data channel
	escaped bool
}

// split appends each received data byte to the buffer of its channel.
func (m *rxMux) split(chunk []byte, data, meter *[]byte) {
	for _, b := range chunk {
		if m.escaped {
			m.escaped = false
			switch {
			case b == escDF:
				m.put(esc, data, meter)
			case b >= chanFirst && b <= chanLast:
				m.ch = int(b - chanFirst)
			default: // DF and another byte: both literal (WG-F13 receive routine)
				m.put(esc, data, meter)
				m.put(b, data, meter)
			}
			continue
		}
		if b == esc {
			m.escaped = true
			continue
		}
		m.put(b, data, meter)
	}
}

func (m *rxMux) put(b byte, data, meter *[]byte) {
	switch m.ch {
	case -1, chanData:
		*data = append(*data, b)
	case chanMeter:
		*meter = append(*meter, b)
	}
}

// escapeOut escapes every DF of a data-channel payload (WG-F13).
func escapeOut(dst, payload []byte) []byte {
	for _, b := range payload {
		dst = append(dst, b)
		if b == esc {
			dst = append(dst, escDF)
		}
	}
	return dst
}

// value is one parameter value as the console keeps it.
type value struct {
	isText  bool
	isFloat bool
	i       int32
	f       float32
	s       string
}

func intVal(i int32) value     { return value{i: i} }
func floatVal(f float32) value { return value{isFloat: true, f: f} }
func textVal(s string) value   { return value{isText: true, s: s} }

// appendValueToken encodes a value the way the console sends it: floats as
// D5, ints inline for 0..63 or D3/D4, strings D0/80..BF/D1 (WG-F30).
func appendValueToken(dst []byte, v value) []byte {
	switch {
	case v.isFloat:
		dst = append(dst, 0xD5)
		return binary.BigEndian.AppendUint32(dst, math.Float32bits(v.f))
	case v.isText:
		n := len(v.s)
		switch {
		case n == 0:
			return append(dst, 0xD0)
		case n <= 64:
			return append(append(dst, byte(0x80+n-1)), v.s...)
		default:
			n = min(n, 256)
			return append(append(dst, 0xD1, byte(n-1)), v.s[:n]...)
		}
	case v.i >= 0 && v.i <= 0x3F:
		return append(dst, byte(v.i))
	case v.i >= math.MinInt16 && v.i <= math.MaxInt16:
		return binary.BigEndian.AppendUint16(append(dst, 0xD3), uint16(int16(v.i)))
	default:
		return binary.BigEndian.AppendUint32(append(dst, 0xD4), uint32(v.i))
	}
}

// appendPush is D7 <hash> <value>: a reply or a push (WG-F31).
func appendPush(dst []byte, hash uint32, v value) []byte {
	dst = binary.BigEndian.AppendUint32(append(dst, 0xD7), hash)
	return appendValueToken(dst, v)
}

// tokenKind of a client token.
type tokenKind uint8

const (
	tkInt tokenKind = iota + 1
	tkIndex
	tkText
	tkName
	tkFloat
	tkRaw
	tkHash
	tkClick
	tkStep
	tkRoot
	tkUp
	tkData
	tkDefs
	tkEnd
	tkDefResp
)

type token struct {
	kind tokenKind
	i    int32
	f    float32
	h    uint32
	b    []byte
}

// nextToken decodes one client token; n = 0 means more bytes are needed.
// Every byte value starts a token (WG-F30); lengths are bounded by 256.
func nextToken(b []byte) (token, int) {
	if len(b) == 0 {
		return token{}, 0
	}
	c := b[0]
	switch {
	case c <= 0x3F:
		return token{kind: tkInt, i: int32(c)}, 1
	case c <= 0x7F:
		return token{kind: tkIndex, i: int32(c-0x40) + 1}, 1
	case c <= 0xBF:
		return sized(b, tkText, 1, int(c-0x80)+1)
	case c <= 0xCF:
		return sized(b, tkName, 1, int(c-0xC0)+1)
	}
	return specialToken(b)
}

func specialToken(b []byte) (token, int) {
	need := map[byte]int{0xD2: 3, 0xD3: 3, 0xD4: 5, 0xD5: 5, 0xD6: 5, 0xD7: 5, 0xD9: 2}
	if n, ok := need[b[0]]; ok && len(b) < n {
		return token{}, 0
	}
	switch b[0] {
	case 0xD0:
		return token{kind: tkText, b: b[1:1]}, 1
	case 0xD1:
		if len(b) < 2 {
			return token{}, 0
		}
		return sized(b, tkText, 2, int(b[1])+1)
	case 0xD2:
		return token{kind: tkIndex, i: int32(binary.BigEndian.Uint16(b[1:])) + 1}, 3
	case 0xD3:
		return token{kind: tkInt, i: int32(int16(binary.BigEndian.Uint16(b[1:])))}, 3
	case 0xD4:
		return token{kind: tkInt, i: int32(binary.BigEndian.Uint32(b[1:]))}, 5
	case 0xD5, 0xD6:
		k := map[byte]tokenKind{0xD5: tkFloat, 0xD6: tkRaw}[b[0]]
		return token{kind: k, f: math.Float32frombits(binary.BigEndian.Uint32(b[1:]))}, 5
	case 0xD7:
		return token{kind: tkHash, h: binary.BigEndian.Uint32(b[1:])}, 5
	case 0xD9:
		return token{kind: tkStep, i: int32(int8(b[1]))}, 2
	case 0xDF:
		return token{kind: tkDefResp}, 1 // only the console sends definitions; skip the byte
	}
	if b[0] > 0xDE {
		return token{}, 1 // E0..FF are not tokens (WG-F30): ignored
	}
	return token{kind: [...]tokenKind{tkClick, 0, tkRoot, tkUp, tkData, tkDefs, tkEnd}[b[0]-0xD8]}, 1
}

func sized(b []byte, k tokenKind, hdr, n int) (token, int) {
	if len(b) < hdr+n {
		return token{}, 0
	}
	return token{kind: k, b: b[hdr : hdr+n]}, hdr + n
}
