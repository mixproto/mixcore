// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package osc

import (
	"testing"
)

// FuzzDecodeMessage asserts that Decode never panics on arbitrary input and that
// every message it accepts re-encodes and decodes to the same message.
func FuzzDecodeMessage(f *testing.F) {
	for _, v := range goldenVectors {
		f.Add(mustHex(f, v.hex))
	}
	f.Add([]byte("/info\x00\x00\x00"))
	f.Add([]byte("/xinfo\x00\x00,\x00\x00\x00"))
	f.Add([]byte("#bundle\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		var m Message
		if err := Decode(b, &m); err != nil {
			return // rejecting is fine; panicking or hanging is not
		}
		out, err := Append(nil, &m)
		if err != nil {
			t.Fatalf("re-encode of a decoded message failed: %v (%s)", err, m.String())
		}
		var again Message
		if err := Decode(out, &again); err != nil || !m.Equal(&again) {
			t.Fatalf("round trip changed the message: %v\n in: %s\nout: %s", err, m.String(), again.String())
		}
	})
}
