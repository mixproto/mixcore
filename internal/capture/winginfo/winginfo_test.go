// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package winginfo

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"go.uber.org/goleak"

	simwing "github.com/mixproto/mixcore/sim/wing"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// TestWalk_ReadOnlyAgainstSim walks /ch/1 and /$syscfg of the simulator,
// with and without a DE after data answers (item 3): the documented hashes
// match and the console logs no write at all.
func TestWalk_ReadOnlyAgainstSim(t *testing.T) {
	for _, de := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			sim := simwing.New(simwing.Options{DataEndsWithDE: de, Model: "wing-rack"})
			client, server := net.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			wg.Go(func() { sim.ServeConn(ctx, server) })
			defer func() { cancel(); _ = client.Close(); wg.Wait() }()
			nodes, stopped, err := Walk(ctx, NewClient(client, 0), Options{Roots: []string{"/ch/1", "/$syscfg"}, Values: true})
			if err != nil || stopped != "done" {
				t.Fatalf("walk: %v %s", err, stopped)
			}
			md := Markdown("10.0.0.1:2222", "privacy", stopped, nodes)
			if !strings.Contains(md, "| /ch/1/fdr | 38ae75c2 | 38ae75c2 |") || !strings.Contains(md, `"wing-rack"`) {
				t.Fatalf("report:\n%s", md)
			}
			if _, err := CSV(nodes); err != nil {
				t.Fatal(err)
			}
			if w := sim.Writes(); len(w) != 0 {
				t.Fatalf("introspection wrote: %+v", w)
			}
		})
	}
}

// TestRequests_OnlyReadOnlyTokens: every byte the client can send decodes to
// DA, D7 <hash>, DD or DC (never a value, D8 or D9).
func TestRequests_OnlyReadOnlyTokens(t *testing.T) {
	for _, r := range []request{{root: true, defs: true}, {hash: 0xDF6A06E2, defs: true}, {hash: 0x38AE75C2}} {
		b := r.encode()
		if b[len(b)-1] != 0xDD && b[len(b)-1] != 0xDC {
			t.Fatalf("% X does not end with DD or DC", b)
		}
		if !r.root && b[0] != 0xD7 || r.root && (len(b) != 2 || b[0] != 0xDA) {
			t.Fatalf("% X", b)
		}
	}
}
