// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"net"
	"testing"
)

// FuzzClientBytes feeds arbitrary client bytes to the simulator's parser:
// it never panics, and a write it applies is never to a read-only leaf.
func FuzzClientBytes(f *testing.F) {
	f.Add(mustHex(f, "DFD1 D738AE75C2DC D738AE75C2D5C1200000 DA C1 6368 41 DD DFD3 D33737 D400000002 DCA001DE"))
	f.Add(mustHex(f, "DFDE DFDF D9FF D8 D2FFFF D1FF E0"))
	f.Add(mustHex(f, "D7 9E69815D 85 6869646465"))
	f.Fuzz(func(t *testing.T, b []byte) {
		s := New(Options{})
		a, z := net.Pipe()
		defer a.Close()
		defer z.Close()
		cl := newClient(s, a, 1)
		s.conns[cl] = struct{}{}
		_ = s.handleBytes(cl, b)
		for _, w := range s.Writes() {
			if n := s.tree.byPath[w.Path]; w.Applied && (n == nil || n.readOnly) {
				t.Fatalf("applied write to %s", w.Path)
			}
		}
	})
}
