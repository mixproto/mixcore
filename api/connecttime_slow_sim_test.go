// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim && slow

package api

import (
	"slices"
	"testing"
	"time"
)

// The reconnect measurement holds each session healthy for 11 s three
// times per family (about 40 s, minutes under -race), so it has its own
// build tag: short harnesses skip it, make sim-test runs it (-tags=sim,slow).

// SR-05: a healthy session loses the link for longer than StaleAfter; it is
// live again within 2 s of the link coming back.
func TestConnectTime_ReconnectLiveWithin2s(t *testing.T) {
	for _, f := range simFamilies {
		t.Run(f.family, func(t *testing.T) {
			t.Parallel() // each family has its own simulator and engine
			sim := f.newSim()
			_, w, began := startOnLoopback(t, f.family, sim)
			if _, ok := w.nextLiveAfter(began); !ok {
				t.Fatal("never live")
			}
			var took []time.Duration
			for range reconnectRuns {
				time.Sleep(healthyFor)
				sim.Pause()
				time.Sleep(reconnectOutage)
				back := time.Now()
				sim.Resume()
				at, ok := w.nextLiveAfter(back)
				if !ok {
					t.Fatalf("not live again within %v", waitLimit)
				}
				took = append(took, at.Sub(back))
			}
			worst := slices.Max(took)
			t.Logf("%s reconnect after a %v outage over %d runs: p50 %v, max %v (limit %v)",
				f.family, reconnectOutage, reconnectRuns, percentile(took, 0.5).Round(time.Millisecond), worst.Round(time.Millisecond), reconnectMax)
			if worst > reconnectMax {
				t.Fatalf("reconnect took %v, limit %v", worst, reconnectMax)
			}
		})
	}
}
