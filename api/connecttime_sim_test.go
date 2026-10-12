// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Connect-time budget (lead decision 2026-10-12, contracts/core-api README):
// against the simulator on loopback, production timing, headamp reads
// included, status live within 2 s at p95 for both families. The test fails
// above 3 s to leave CI a margin; the 5 s connect timeout stays the hard
// failure. Reconnect: live again within 2 s after the link returns (SR-05).
const (
	connectRuns     = 20
	connectTarget   = 2 * time.Second
	connectCIMax    = 3 * time.Second
	reconnectRuns   = 3
	reconnectMax    = 2 * time.Second
	reconnectOutage = 2 * time.Second // longer than StaleAfter (1.5 s): the engine really reconnects
	// healthyFor: SR-05 is about a healthy session losing its link. A session
	// shorter than the supervisor's 10 s stableAfter counts as a failed
	// attempt and is backed off on purpose (console/supervisor.go).
	healthyFor = 11 * time.Second
	waitLimit  = 10 * time.Second
)

// simServer is a family simulator the test can pause.
type simServer interface {
	Serve(ctx context.Context, pc net.PacketConn) error
	Pause()
	Resume()
}

var simFamilies = []struct {
	family string
	newSim func() simServer
}{
	{"x32", func() simServer { return simx32.New(simx32.Options{}) }},
	{"xair", func() simServer { return simxair.New(simxair.Options{}) }},
}

// liveWatch records when each state message with status live arrives.
type liveWatch struct {
	mu   sync.Mutex
	live []time.Time
	ch   chan struct{}
}

func newLiveWatch() *liveWatch { return &liveWatch{ch: make(chan struct{}, 64)} }

func (w *liveWatch) OnState(s string) {
	var m struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(s), &m) != nil || m.Status != "live" {
		return
	}
	w.mu.Lock()
	w.live = append(w.live, time.Now())
	w.mu.Unlock()
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

// nextLiveAfter waits for a live message received after t.
func (w *liveWatch) nextLiveAfter(t time.Time) (time.Time, bool) {
	deadline := time.After(waitLimit)
	for {
		w.mu.Lock()
		for _, at := range w.live {
			if at.After(t) {
				w.mu.Unlock()
				return at, true
			}
		}
		w.mu.Unlock()
		select {
		case <-w.ch:
		case <-deadline:
			return time.Time{}, false
		}
	}
}

// startOnLoopback serves a fresh simulator on 127.0.0.1 and starts a fresh
// engine with the built-in drivers (production timing). It returns when
// Start returned; the engine connects in the background.
func startOnLoopback(t *testing.T, family string, sim simServer) (*Engine, *liveWatch, time.Time) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	eng := NewEngineWith(Options{})
	w := newLiveWatch()
	eng.SetStateListener(w)
	t.Cleanup(func() { _ = eng.Stop(); cancel(); wg.Wait(); _ = pc.Close() })
	cfg := fmt.Sprintf(`{"version":0,"family":%q,"host":"127.0.0.1","port":%d}`, family, pc.LocalAddr().(*net.UDPAddr).Port)
	began := time.Now()
	if _, err := eng.Start(cfg); err != nil {
		t.Fatal(err)
	}
	return eng, w, began
}

func percentile(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(float64(len(s)-1)*p + 0.5)
	return s[i]
}

func TestConnectTime_FirstConnectLiveWithin2s(t *testing.T) {
	for _, f := range simFamilies {
		t.Run(f.family, func(t *testing.T) {
			var took []time.Duration
			for range connectRuns {
				eng, w, began := startOnLoopback(t, f.family, f.newSim())
				at, ok := w.nextLiveAfter(began)
				if !ok {
					t.Fatalf("not live within %v", waitLimit)
				}
				took = append(took, at.Sub(began))
				_ = eng.Stop()
			}
			p50, p95, worst := percentile(took, 0.5), percentile(took, 0.95), slices.Max(took)
			t.Logf("%s first connect over %d runs: p50 %v, p95 %v, max %v (target p95 <= %v)",
				f.family, connectRuns, p50.Round(time.Millisecond), p95.Round(time.Millisecond), worst.Round(time.Millisecond), connectTarget)
			if p95 > connectCIMax {
				t.Fatalf("p95 %v above the CI limit %v (target %v)", p95, connectCIMax, connectTarget)
			}
		})
	}
}
