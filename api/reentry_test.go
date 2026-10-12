// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/memnet"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// reentrant calls Stop, Pause and StopDemo from inside OnState once live.
type reentrant struct {
	eng  *Engine
	once sync.Once
	got  chan [3]error
}

func (r *reentrant) OnState(s string) {
	if strings.Contains(s, `"status":"live"`) {
		r.once.Do(func() { r.got <- [3]error{r.eng.Stop(), r.eng.Pause(), r.eng.StopDemo()} })
	}
}

// Stop, Pause and StopDemo from inside OnState would wait for the very
// goroutine that runs OnState. They return ERR_BUSY at once and change
// nothing; the same calls from the app's own thread then work, and so does
// a later session to a real console.
func TestOnState_StopPauseStopDemoAreBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		pc, err := n.ListenPacket(consoleIP + ":10023")
		if err != nil {
			t.Fatal(err)
		}
		sim := simx32.New(simx32.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { _ = sim.Serve(ctx, pc) })
		defer func() { cancel(); wg.Wait(); _ = pc.Close() }()
		eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2"), DemoListenPacket: n.Host("127.0.0.1")})
		defer eng.Close()
		r := &reentrant{eng: eng, got: make(chan [3]error, 1)}
		eng.SetStateListener(r)
		out, err := eng.StartDemo(`{"version":0,"family":"x32"}`)
		if err != nil {
			t.Fatal(err)
		}
		var m demoMixerJSON
		_ = decodeStrict(out, &m)
		if _, err := eng.Start(demoConfig(m)); err != nil {
			t.Fatal(err)
		}
		select {
		case errs := <-r.got:
			for i, err := range errs {
				if CodeOf(err) != CodeBusy {
					t.Fatalf("call %d (Stop, Pause, StopDemo) from OnState = %v, want ERR_BUSY", i, err)
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a call from OnState blocked")
		}
		eng.SetStateListener(&recorder{})
		if err := eng.Pause(); err != nil {
			t.Fatalf("Pause from the app: %v", err)
		}
		if err := eng.StopDemo(); err != nil {
			t.Fatalf("StopDemo from the app: %v", err)
		}
		if _, err := eng.StartDemo(`{"version":0,"family":"x32"}`); err != nil {
			t.Fatalf("StartDemo again: %v", err)
		}
		if _, err := eng.Start(configOK); err != nil {
			t.Fatalf("Start a real console: %v", err)
		}
		time.Sleep(time.Second)
		if err := eng.Stop(); err != nil {
			t.Fatalf("Stop a real console: %v", err)
		}
	})
}

func TestGoroutineID(t *testing.T) {
	a := goroutineID()
	b := make(chan int64)
	go func() { b <- goroutineID() }()
	if other := <-b; a <= 0 || other <= 0 || a == other || a != goroutineID() {
		t.Fatalf("ids %d, %d", a, other)
	}
}

func BenchmarkGoroutineID(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = goroutineID()
	}
}

// overlapProbe counts OnState calls running at the same time.
type overlapProbe struct {
	running, worst atomic.Int32
}

func (p *overlapProbe) OnState(string) {
	n := p.running.Add(1)
	for w := p.worst.Load(); n > w && !p.worst.CompareAndSwap(w, n); w = p.worst.Load() {
	}
	time.Sleep(50 * time.Microsecond) // widen the window a racing dispatcher would hit
	p.running.Add(-1)
}

// Start and Stop from two app threads at once: a stopping session's last
// message and the next session's first never reach the listener at the
// same time (README: OnState runs one message at a time), and the
// dispatchers share no unguarded state (-race).
func TestOnState_RacingStartStopNeverOverlap(t *testing.T) {
	const rounds = 300
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":10023")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	defer func() { cancel(); wg.Wait(); _ = pc.Close() }()
	eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2")})
	defer eng.Close()
	probe := &overlapProbe{}
	eng.SetStateListener(probe)
	var apps sync.WaitGroup
	for range 2 {
		apps.Go(func() {
			for range rounds {
				_, _ = eng.Start(`{"version":0,"family":"x32","host":"10.0.0.1","safety":{"showMode":true}}`)
				_ = eng.Stop()
			}
		})
	}
	apps.Wait()
	if w := probe.worst.Load(); w > 1 {
		t.Fatalf("%d OnState calls ran at the same time", w)
	}
}

// Stop on one thread races Start of a demo session on another: the stopping
// real console's final message is encoded after the demo session started,
// and must still carry its own flag (no "demo").
func TestDeliver_FinalMessageKeepsItsSessionsDemoFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		eng, rec := newRealAndDemoEngine(t)
		if _, err := eng.Start(configOK); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		out, err := eng.StartDemo(`{"version":0,"family":"x32"}`)
		if err != nil {
			t.Fatal(err)
		}
		var m demoMixerJSON
		_ = decodeStrict(out, &m)
		hit, release := make(chan struct{}), make(chan struct{})
		hook := func() { hit <- struct{}{}; <-release }
		eng.afterGenCheck.Store(&hook)
		stopped := make(chan error, 1)
		go func() { stopped <- eng.Stop() }()
		<-hit // the real console's final delivery passed its generation check
		eng.afterGenCheck.Store(nil)
		if _, err := eng.Start(demoConfig(m)); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		for _, raw := range rec.raw {
			if strings.Contains(raw, `"status":"stopped"`) && strings.Contains(raw, `"demo"`) {
				t.Fatalf("the real console's final message says demo: %s", raw)
			}
		}
	})
}

// newRealAndDemoEngine runs a real console simulator at consoleIP and an
// engine whose demo uses the same in-memory network (call inside synctest).
func newRealAndDemoEngine(t *testing.T) (*Engine, *recorder) {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":10023")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2"), DemoListenPacket: n.Host("127.0.0.1")})
	rec := &recorder{}
	eng.SetStateListener(rec)
	t.Cleanup(func() { eng.Close(); cancel(); wg.Wait(); _ = pc.Close() })
	return eng, rec
}
