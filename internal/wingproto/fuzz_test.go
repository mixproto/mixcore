// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzStream feeds arbitrary bytes through the demux and the token parser as
// a reader would: malformed streams never panic or loop, and a definition
// token's body never exceeds MaxDefLen.
func FuzzStream(f *testing.F) {
	f.Add(mustHex(f, "DFD1 D738AE75C2 D5C3100000"))             // push: ch 1 fader -oo (WG-F60)
	f.Add(mustHex(f, "D702DFDEAF0E02"))                         // WG-F13 example
	f.Add(mustHex(f, "DFD1 DF DE 0010 00000000 70B10139 0001")) // a truncated definition
	f.Add(mustHex(f, "DFDE 0000 7FFFFFFF"))                     // an absurd long definition length
	f.Fuzz(func(t *testing.T, in []byte) {
		var d Demux
		data := d.Append(nil, in, ChannelData)
		for len(data) > 0 {
			tok, n, err := Parse(data)
			if errors.Is(err, ErrShort) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrUnknownToken) {
				return
			}
			if err != nil || n <= 0 || n > len(data) {
				t.Fatalf("Parse: n=%d err=%v of %d bytes", n, err, len(data))
			}
			if tok.Kind == KindDef {
				if len(tok.Bytes) > MaxDefLen {
					t.Fatalf("definition of %d bytes", len(tok.Bytes))
				}
				_, _ = ParseDef(tok.Bytes)
			}
			data = data[n:]
		}
	})
}

// FuzzEscapeRoundTrip: any payload framed with AppendFrame comes back
// unchanged on its channel (WG-F13: every 0xDF escaped, WG-F103).
func FuzzEscapeRoundTrip(f *testing.F) {
	f.Add([]byte{0x44, 0xDF, 0xD0, 0x00}, 1)
	f.Add([]byte{0xDF, 0xDE, 0xDF}, 3)
	f.Fuzz(func(t *testing.T, payload []byte, ch int) {
		ch = ((ch % (MaxChannel + 1)) + MaxChannel + 1) % (MaxChannel + 1)
		var d Demux
		got := d.Append(nil, AppendFrame(nil, ch, payload), ch)
		if !bytes.Equal(got, payload) {
			t.Fatalf("channel %d: % X came back as % X", ch, payload, got)
		}
	})
}

// FuzzParseDef: arbitrary definition bodies never panic; a body that parses
// re-encodes to a body that parses to the same definition.
func FuzzParseDef(f *testing.F) {
	body, _ := AppendDefBody(nil, Def{Parent: 1, Hash: 2, Name: "grp", Type: TypeStringEnum, Items: []EnumItem{{Name: "LCL", Long: "Local"}}})
	f.Add(body)
	f.Fuzz(func(t *testing.T, in []byte) {
		d, err := ParseDef(in)
		if err != nil {
			return
		}
		out, err := AppendDefBody(nil, d)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		again, err := ParseDef(out)
		if err != nil || again.Hash != d.Hash || again.Name != d.Name || len(again.Items) != len(d.Items) {
			t.Fatalf("round trip changed the definition: %v", err)
		}
	})
}
