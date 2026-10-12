// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const (
	latencySamples = 1000
	latencyBudget  = 20 * time.Millisecond  // fader touch -> packet budget (qa-reliability-standards section 8)
	applyBudget    = 500 * time.Microsecond // UI -> core API call budget, p95
)

var fastTiming = x32.Timing{
	RenewEvery: 300 * time.Millisecond, PingEvery: 100 * time.Millisecond, DeadAfter: 400 * time.Millisecond,
	ConnectTimeout: 2 * time.Second, ReadRetryEvery: 100 * time.Millisecond, EchoWindow: time.Second,
}

type loop struct {
	sim *simx32.Sim
	eng *Engine
	rec *recorder
	cfg string
}

// newLoop runs the simulator on a real loopback UDP socket and an engine with
// scaled timing (driver and simulator lifetimes scaled together).
func newLoop(t *testing.T) *loop {
	t.Helper()
	return newLoopWith(t, Options{Drivers: map[string]driver.Driver{"x32": x32.New(x32.Config{Timing: fastTiming})}})
}

// newLoopWith is newLoop with explicit engine options (fast timing is added
// when no driver is given).
func newLoopWith(t *testing.T, opts Options) *loop {
	t.Helper()
	if opts.Drivers == nil {
		opts.Drivers = map[string]driver.Driver{"x32": x32.New(x32.Config{Timing: fastTiming})}
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{SubscriptionTTL: time.Second, MeterTTL: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	eng := NewEngineWith(opts)
	rec := &recorder{}
	eng.SetStateListener(rec)
	t.Cleanup(func() { _ = eng.Stop(); cancel(); wg.Wait(); _ = pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	cfg := fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d}`, port)
	return &loop{sim: sim, eng: eng, rec: rec, cfg: cfg}
}

func (l *loop) waitStatus(t *testing.T, status string) stateJSON {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := l.rec.last(); st.Status == status {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status never became %q (last %+v)", status, l.rec.last())
	return stateJSON{}
}

func TestSimLoopback_RebootDropsQueuedWritesAndReplacesState(t *testing.T) {
	l := newLoop(t)
	if _, err := l.eng.Start(l.cfg); err != nil {
		t.Fatal(err)
	}
	l.waitStatus(t, "live")
	if err := l.eng.Apply(cmd("fader", "-5", "gesture")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	before := l.sim.WriteCount()

	l.sim.Reboot(time.Second)
	_ = l.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.25)) // the desk moved during the reboot
	l.waitStatus(t, "stale")
	for range 10 {
		if err := l.eng.Apply(cmd("fader", "-1", "gesture")); CodeOf(err) != CodeStale {
			t.Fatalf("Apply while stale = %v", err)
		}
	}
	st := l.waitStatus(t, "live")
	if st.Type != "snapshot" || st.Strips[0].FaderDB.(float64) > -29.9 || st.Strips[0].FaderDB.(float64) < -30.1 {
		t.Fatalf("after reconnect ch1 = %v, want the console's -30 dB", st.Strips[0].FaderDB)
	}
	time.Sleep(500 * time.Millisecond)
	if after := l.sim.WriteCount(); after != before {
		t.Fatalf("%d writes reached the console after the reboot; queued writes must be dropped", after-before)
	}
}

func TestSimLoopback_ResumeRereadsAndWritesNothing(t *testing.T) {
	l := newLoop(t)
	if _, err := l.eng.Start(l.cfg); err != nil {
		t.Fatal(err)
	}
	l.waitStatus(t, "live")
	if err := l.eng.Pause(); err != nil {
		t.Fatal(err)
	}
	l.waitStatus(t, "stopped")
	_ = l.sim.SurfaceSet("/ch/03/config/name", osc.String("Resumed"))
	if err := l.eng.Resume(); err != nil {
		t.Fatal(err)
	}
	st := l.waitStatus(t, "live")
	if st.Strips[2].Name != "Resumed" {
		t.Fatalf("Resume did not re-read: ch3 = %q", st.Strips[2].Name)
	}
	if n := len(l.sim.Writes()); n != 0 {
		t.Fatalf("Pause/Resume wrote %d times", n)
	}
}

// TestSimLoopback_ApplyLatency measures Engine.Apply (JSON in) -> simulator
// receipt over real loopback UDP, one gesture at a time.
func TestSimLoopback_ApplyLatency(t *testing.T) {
	l := newLoop(t)
	if _, err := l.eng.Start(l.cfg); err != nil {
		t.Fatal(err)
	}
	l.waitStatus(t, "live")
	sent := make([]time.Time, latencySamples)
	calls := make([]time.Duration, latencySamples)
	for i := range latencySamples {
		db := strconv.Itoa(-(i % 40))
		sent[i] = time.Now()
		if err := l.eng.Apply(cmd("fader", db, "gesture")); err != nil {
			t.Fatal(err)
		}
		calls[i] = time.Since(sent[i])
		for l.sim.WriteCount() <= i {
			time.Sleep(20 * time.Microsecond)
		}
	}
	writes := l.sim.Writes()
	lat := make([]time.Duration, latencySamples)
	for i, w := range writes[:latencySamples] {
		lat[i] = w.At.Sub(sent[i])
	}
	p := percentiles(lat)
	c := percentiles(calls)
	t.Logf("Apply -> simulator receipt, loopback UDP, n=%d: p50=%v p95=%v p99=%v max=%v", latencySamples, p[0], p[1], p[2], p[3])
	t.Logf("Apply call duration (caller thread), n=%d: p50=%v p95=%v p99=%v max=%v", latencySamples, c[0], c[1], c[2], c[3])
	if p[1] > latencyBudget {
		t.Fatalf("p95 %v exceeds the %v budget", p[1], latencyBudget)
	}
	if c[1] > applyBudget {
		t.Fatalf("Apply call p95 %v exceeds the %v budget", c[1], applyBudget)
	}
}

// percentiles returns p50, p95, p99 and max.
func percentiles(d []time.Duration) [4]time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(q float64) time.Duration { return s[min(int(q*float64(len(s))), len(s)-1)] }
	return [4]time.Duration{at(0.50), at(0.95), at(0.99), s[len(s)-1]}
}

// An x32 session carries the main strip's mute-group membership end to end:
// /main/st/grp/mute (X32-F36) -> Capabilities.MainMuteGroups -> the main
// strip's muteGroups in the snapshot and its effectiveMute (xair omits it).
func TestSimLoopback_X32MainCarriesMuteGroups(t *testing.T) {
	l := newLoop(t)
	if err := l.sim.SurfaceSet("/main/st/grp/mute", osc.Int(1<<2)); err != nil { // main in mute group 3
		t.Fatal(err)
	}
	if err := l.sim.SurfaceSet("/config/mute/3", osc.Int(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.eng.Start(l.cfg); err != nil {
		t.Fatal(err)
	}
	l.waitStatus(t, "live")
	if err := l.eng.RequestSnapshot(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := l.rec.last()
		if st.Type == "snapshot" && len(st.Strips) == 33 {
			main := st.Strips[32]
			if main.Kind != "main" || main.MuteGroups == nil || len(*main.MuteGroups) != 1 || (*main.MuteGroups)[0] != 3 ||
				main.EffectiveMute == nil || !main.EffectiveMute.Muted {
				t.Fatalf("main = %+v", main)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no 33-strip snapshot; last %+v", l.rec.last())
}
