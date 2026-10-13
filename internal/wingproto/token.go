// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Kind is the kind of a data-channel token (WG-F30).
type Kind uint8

// Token kinds (WG-F30).
const (
	KindInt         Kind = iota + 1 // 00..3F inline (00 false, 01 true), D3 int16, D4 int32
	KindNodeIndex                   // 40..7F index 1..64, D2 index 1..65536
	KindString                      // D0 empty, 80..BF 1..64 bytes, D1 1..256 bytes
	KindNodeName                    // C0..CF 1..16 bytes
	KindFloat                       // D5 float32 in the parameter's unit (dB, Hz, ms, %)
	KindRawFloat                    // D6 float32 raw 0.0..1.0
	KindHash                        // D7 node hash
	KindClick                       // D8 toggle (mixcore never sends it)
	KindStep                        // D9 int8 step (mixcore never sends it)
	KindRoot                        // DA go to root
	KindUp                          // DB one level up
	KindDataRequest                 // DC
	KindDefRequest                  // DD
	KindEnd                         // DE end of data or definition request
	KindDef                         // DF node definition response (WG-F33)
)

// Token bytes (WG-F30).
const (
	tokIntMax      byte = 0x3F
	tokIndexBase   byte = 0x40
	tokIndexMax    byte = 0x7F
	tokStrBase     byte = 0x80
	tokStrMax      byte = 0xBF
	tokNameBase    byte = 0xC0
	tokNameMax     byte = 0xCF
	tokEmptyString byte = 0xD0
	tokLongString  byte = 0xD1
	tokIndex16     byte = 0xD2
	tokInt16       byte = 0xD3
	tokInt32       byte = 0xD4
	tokFloat       byte = 0xD5
	tokRawFloat    byte = 0xD6
	tokHash        byte = 0xD7
	tokClick       byte = 0xD8
	tokStep        byte = 0xD9
	tokRoot        byte = 0xDA
	tokUp          byte = 0xDB
	tokData        byte = 0xDC
	tokDef         byte = 0xDD
	tokEnd         byte = 0xDE
	tokDefResponse byte = 0xDF
)

// Limits (WG-F30, WG-F33).
const (
	// MaxShortString is the longest string of one 80..BF token.
	MaxShortString = 64
	// MaxString is the longest string of any token (D1, 1..256 bytes).
	MaxString = 256
	// MaxDefLen caps a node definition body. Real definitions are far
	// smaller (the largest are enums of a few dozen items); a longer one is
	// rejected, never buffered.
	MaxDefLen = 64 << 10
)

// Errors of Parse.
var (
	// ErrShort means b ends inside a token: wait for more bytes.
	ErrShort = errors.New("wingproto: incomplete token")
	// ErrTooLarge means a definition is longer than MaxDefLen: the stream
	// cannot be resynchronised and must be dropped.
	ErrTooLarge = errors.New("wingproto: node definition too large")
	// ErrValue means a value cannot be encoded (a string too long).
	ErrValue = errors.New("wingproto: value cannot be encoded")
	// ErrUnknownToken means a byte E0..FF where a token starts: WG-F30
	// defines 00..DF only, so its length is unknown and the stream cannot
	// be resynchronised. A reader must drop the stream, never guess: the
	// bytes after it could be misread as a hash and a value.
	ErrUnknownToken = errors.New("wingproto: undefined token byte")
)

// Token is one decoded token. Bytes aliases the parsed input: copy it before
// the input buffer is reused.
type Token struct {
	Kind  Kind
	Int   int32   // KindInt, KindNodeIndex (1-based), KindStep
	Float float32 // KindFloat, KindRawFloat
	Hash  uint32  // KindHash
	Bytes []byte  // KindString, KindNodeName, KindDef (the body after the length)
}

// Parse decodes the token at the start of b (data-channel bytes, already
// demuxed and unescaped) and returns it with the number of bytes it used.
// WG-F30 defines tokens 00..DF; the errors are ErrShort, ErrTooLarge and
// ErrUnknownToken (E0..FF).
func Parse(b []byte) (Token, int, error) {
	if len(b) == 0 {
		return Token{}, 0, ErrShort
	}
	c := b[0]
	switch {
	case c <= tokIntMax:
		return Token{Kind: KindInt, Int: int32(c)}, 1, nil
	case c <= tokIndexMax:
		return Token{Kind: KindNodeIndex, Int: int32(c-tokIndexBase) + 1}, 1, nil
	case c <= tokStrMax:
		return bytesToken(b, KindString, 1, int(c-tokStrBase)+1)
	case c <= tokNameMax:
		return bytesToken(b, KindNodeName, 1, int(c-tokNameBase)+1)
	}
	return parseSpecial(b)
}

// parseSpecial decodes the D0..DF tokens.
func parseSpecial(b []byte) (Token, int, error) {
	switch b[0] {
	case tokEmptyString:
		return Token{Kind: KindString, Bytes: b[1:1]}, 1, nil
	case tokLongString:
		if len(b) < 2 {
			return Token{}, 0, ErrShort
		}
		return bytesToken(b, KindString, 2, int(b[1])+1)
	case tokIndex16:
		v, err := fixed(b, 2)
		return Token{Kind: KindNodeIndex, Int: int32(v) + 1}, 3, err
	case tokInt16:
		v, err := fixed(b, 2)
		return Token{Kind: KindInt, Int: int32(int16(v))}, 3, err
	case tokInt32:
		v, err := fixed(b, 4)
		return Token{Kind: KindInt, Int: int32(v)}, 5, err
	case tokFloat, tokRawFloat:
		v, err := fixed(b, 4)
		k := KindFloat
		if b[0] == tokRawFloat {
			k = KindRawFloat
		}
		return Token{Kind: k, Float: math.Float32frombits(v)}, 5, err
	case tokHash:
		v, err := fixed(b, 4)
		return Token{Kind: KindHash, Hash: v}, 5, err
	case tokStep:
		if len(b) < 2 {
			return Token{}, 0, ErrShort
		}
		return Token{Kind: KindStep, Int: int32(int8(b[1]))}, 2, nil
	case tokDefResponse:
		return parseDefToken(b)
	}
	if b[0] > tokEnd {
		return Token{}, 0, fmt.Errorf("%w: %#x", ErrUnknownToken, b[0])
	}
	return Token{Kind: simpleKinds[b[0]-tokClick]}, 1, nil
}

// simpleKinds are the one-byte tokens D8..DE.
var simpleKinds = [...]Kind{KindClick, KindStep, KindRoot, KindUp, KindDataRequest, KindDefRequest, KindEnd}

// fixed reads a big-endian word (n = 2) or long (n = 4) after the token byte.
func fixed(b []byte, n int) (uint32, error) {
	if len(b) < 1+n {
		return 0, ErrShort
	}
	if n == 2 {
		return uint32(binary.BigEndian.Uint16(b[1:3])), nil
	}
	return binary.BigEndian.Uint32(b[1:5]), nil
}

// bytesToken is a token of n bytes after a header of hdr bytes.
func bytesToken(b []byte, k Kind, hdr, n int) (Token, int, error) {
	if len(b) < hdr+n {
		return Token{}, 0, ErrShort
	}
	return Token{Kind: k, Bytes: b[hdr : hdr+n]}, hdr + n, nil
}

// parseDefToken decodes DF len.w [len.l if len.w = 0] body (WG-F33).
func parseDefToken(b []byte) (Token, int, error) {
	if len(b) < 3 {
		return Token{}, 0, ErrShort
	}
	n, hdr := int(binary.BigEndian.Uint16(b[1:3])), 3
	if n == 0 {
		if len(b) < 7 {
			return Token{}, 0, ErrShort
		}
		long := binary.BigEndian.Uint32(b[3:7])
		if long > MaxDefLen {
			return Token{}, 0, fmt.Errorf("%w: %d bytes", ErrTooLarge, long)
		}
		n, hdr = int(long), 7
	}
	if n > MaxDefLen {
		return Token{}, 0, fmt.Errorf("%w: %d bytes", ErrTooLarge, n)
	}
	return bytesToken(b, KindDef, hdr, n)
}
