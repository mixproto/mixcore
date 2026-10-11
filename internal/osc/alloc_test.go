// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package osc

import "testing"

// The race detector changes allocation counts, so the zero-alloc budget is
// enforced only in non-race builds.
func TestZeroAlloc_EncodeAndDecode(t *testing.T) {
	buf := make([]byte, 0, 64)
	if n := testing.AllocsPerRun(200, func() {
		var err error
		if buf, err = encodeFaderSet(buf); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Fatalf("fader SET encode allocates %v times per run, budget is 0", n)
	}

	pkt := mustHex(t, goldenVectors[len(goldenVectors)-1].hex)
	var m Message
	if n := testing.AllocsPerRun(200, func() {
		if err := Decode(pkt, &m); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Fatalf("decode allocates %v times per run, budget is 0", n)
	}
}
