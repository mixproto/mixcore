// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package mobile

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const (
	callSamples = 10000
	// callBudget is the per-call budget of Apply and MeterFrame through the
	// binding's Go side (spike S3 target: p95 < 0.5 ms).
	callBudget = 500 * time.Microsecond
)

type stateWaiter struct {
	mu   sync.Mutex
	last string
}

func (w *stateWaiter) OnState(s string) {
	w.mu.Lock()
	w.last = s
	w.mu.Unlock()
}

func (w *stateWaiter) waitLive(t testing.TB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		live := strings.Contains(w.last, `"status":"live"`)
		w.mu.Unlock()
		if live {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("engine never went live")
}

// startLive serves a simulator on loopback with input meters moving and starts
// the package engine against it.
func startLive(t testing.TB) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{})
	levels := make([]float32, 32)
	for i := range levels {
		levels[i] = 0.5
	}
	sim.SetInputLevels(levels)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	w := &stateWaiter{}
	SetStateListener(w)
	cfg := fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d,"meters":{"inputs":true}}`,
		pc.LocalAddr().(*net.UDPAddr).Port)
	if _, err := Start(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = Stop()
		SetStateListener(nil)
		cancel()
		wg.Wait()
		_ = pc.Close()
	})
	w.waitLive(t)
	for len(MeterFrame()) == 24 { // wait for the first meter packet
		time.Sleep(5 * time.Millisecond)
	}
}

func faderCmd(i int) string {
	return fmt.Sprintf(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":%d,`+
		`"origin":{"kind":"gesture","clientId":"bench","seq":%d}}`, -(i % 40), i+1)
}

func quantiles(d []time.Duration) string {
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(q float64) time.Duration { return s[min(int(q*float64(len(s))), len(s)-1)] }
	return fmt.Sprintf("p50=%v p95=%v p99=%v max=%v", at(0.50), at(0.95), at(0.99), s[len(s)-1])
}

func p95(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[int(0.95*float64(len(s)))]
}

// TestSimLoopback_BindingCallCost measures the Go side of the gomobile
// binding (S3): one call of Apply and of MeterFrame on a live engine.
func TestSimLoopback_BindingCallCost(t *testing.T) {
	startLive(t)
	applies := make([]time.Duration, callSamples)
	frames := make([]time.Duration, callSamples)
	for i := range callSamples {
		start := time.Now()
		if err := Apply(faderCmd(i)); err != nil {
			t.Fatalf("Apply %d: %v", i, err)
		}
		applies[i] = time.Since(start)
		start = time.Now()
		if b := MeterFrame(); len(b) != 24+50*4 { // v0.5: 16 bus masters before Main L/R
			t.Fatalf("frame of %d bytes", len(b))
		}
		frames[i] = time.Since(start)
	}
	t.Logf("mobile.Apply, n=%d: %s", callSamples, quantiles(applies))
	t.Logf("mobile.MeterFrame, n=%d: %s", callSamples, quantiles(frames))
	if p := p95(applies); p > callBudget {
		t.Fatalf("Apply p95 %v exceeds %v", p, callBudget)
	}
	if p := p95(frames); p > callBudget {
		t.Fatalf("MeterFrame p95 %v exceeds %v", p, callBudget)
	}
}

func BenchmarkApply(b *testing.B) {
	startLive(b)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if err := Apply(faderCmd(i)); err != nil {
			b.Fatal(err)
		}
		i++
	}
}

func BenchmarkMeterFrame(b *testing.B) {
	startLive(b)
	b.ReportAllocs()
	for b.Loop() {
		if len(MeterFrame()) == 0 {
			b.Fatal("empty frame")
		}
	}
}
