// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package wing

import (
	"slices"
	"testing"
	"time"

	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestConnectTime_Loopback measures Link.Connect against sim/wing on real
// loopback sockets: the first connect of a link and later reconnects
// (median of connectRuns). It reports; it fails only past connectBudget
// (the north star: input unlocked within 2 s of the network returning).
func TestConnectTime_Loopback(t *testing.T) {
	const (
		connectRuns   = 20
		connectBudget = time.Second
	)
	sim, ep := loopback(t, simwing.Options{})
	_ = sim
	first, again := []time.Duration{}, []time.Duration{}
	for range connectRuns {
		l, err := New(Config{}).OpenLink(t.Context(), ep, optsNone())
		if err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			start := time.Now()
			s, err := l.Connect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			took := time.Since(start)
			_ = s.Close()
			if i == 0 {
				first = append(first, took)
			} else {
				again = append(again, took)
			}
		}
		_ = l.Close()
	}
	slices.Sort(first)
	slices.Sort(again)
	t.Logf("Link.Connect on loopback, median of %d: first %v, reconnect %v (max %v, %v)",
		connectRuns, first[connectRuns/2], again[connectRuns/2], first[connectRuns-1], again[connectRuns-1])
	if first[connectRuns/2] > connectBudget || again[connectRuns/2] > connectBudget {
		t.Fatal("connect over budget")
	}
}
