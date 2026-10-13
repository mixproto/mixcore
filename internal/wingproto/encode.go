// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The encoders below append raw data-channel tokens (WG-F30). Bytes on the
// wire are the result of AppendFrame, which selects the channel and escapes
// every 0xDF (WG-F13). mixcore never encodes the toggle (D8) or step (D9)
// tokens: a lost or repeated toggle inverts the user's intent (wing.md 2.4).

// AppendFrame appends a channel selection and payload with every 0xDF escaped.
func AppendFrame(dst []byte, ch int, payload []byte) []byte {
	return AppendEscaped(AppendSelect(dst, ch), payload)
}

// AppendHash selects a node by hash: D7 <u32>.
func AppendHash(dst []byte, h uint32) []byte {
	return binary.BigEndian.AppendUint32(append(dst, tokHash), h)
}

// AppendRoot goes to the root of the tree: DA.
func AppendRoot(dst []byte) []byte { return append(dst, tokRoot) }

// AppendDataRequest asks for the data of the selected node: DC (WG-F31).
func AppendDataRequest(dst []byte) []byte { return append(dst, tokData) }

// AppendDefRequest asks for the definitions of the selected node's
// children: DD (WG-F31).
func AppendDefRequest(dst []byte) []byte { return append(dst, tokDef) }

// AppendEnd ends a request: DE.
func AppendEnd(dst []byte) []byte { return append(dst, tokEnd) }

// AppendInt appends an integer value: 00..3F inline for 0..63, D3 for the
// rest of the int16 range, D4 otherwise (WG-F30, WG-F65). 0 and 1 are also
// false and true.
func AppendInt(dst []byte, v int32) []byte {
	switch {
	case v >= 0 && v <= int32(tokIntMax):
		return append(dst, byte(v))
	case v >= math.MinInt16 && v <= math.MaxInt16:
		return binary.BigEndian.AppendUint16(append(dst, tokInt16), uint16(int16(v)))
	default:
		return binary.BigEndian.AppendUint32(append(dst, tokInt32), uint32(v))
	}
}

// AppendBool appends 01 for true, 00 for false (WG-F30).
func AppendBool(dst []byte, on bool) []byte {
	if on {
		return append(dst, 1)
	}
	return append(dst, 0)
}

// AppendFloat appends a value in the parameter's unit: D5 <f32> (WG-F30,
// WG-F60: faders are written in dB).
func AppendFloat(dst []byte, f float32) []byte {
	return binary.BigEndian.AppendUint32(append(dst, tokFloat), math.Float32bits(f))
}

// AppendString appends a string: D0 when empty, 80..BF for 1..64 bytes, D1
// for 65..256 bytes (WG-F30). Longer strings are ErrValue.
func AppendString(dst []byte, s string) ([]byte, error) {
	switch n := len(s); {
	case n == 0:
		return append(dst, tokEmptyString), nil
	case n <= MaxShortString:
		return append(append(dst, tokStrBase+byte(n-1)), s...), nil
	case n <= MaxString:
		return append(append(dst, tokLongString, byte(n-1)), s...), nil
	default:
		return dst, fmt.Errorf("%w: string of %d bytes (max %d)", ErrValue, n, MaxString)
	}
}

// AppendValueRead asks for the value of one node by hash: D7 <hash> DC.
func AppendValueRead(dst []byte, h uint32) []byte { return AppendDataRequest(AppendHash(dst, h)) }

// AppendDefsOf asks for the definitions of the children of one node: D7
// <hash> DD.
func AppendDefsOf(dst []byte, h uint32) []byte { return AppendDefRequest(AppendHash(dst, h)) }
