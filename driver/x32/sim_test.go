// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package x32

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// Scaled timing: driver and simulator get the same 10x shorter lifetimes.
var (
	fastTiming = Timing{
		RenewEvery: 300 * time.Millisecond, PingEvery: 100 * time.Millisecond, DeadAfter: 400 * time.Millisecond,
		ConnectTimeout: 2 * time.Second, ReadRetryEvery: 100 * time.Millisecond, EchoWindow: time.Second,
	}
	fastSim = simx32.Options{SubscriptionTTL: time.Second, MeterTTL: time.Second}
)

// loopback serves a simulator on a real UDP socket on 127.0.0.1.
func loopback(t *testing.T, opts simx32.Options) (*simx32.Sim, driver.Endpoint) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	return sim, driver.Endpoint{Host: "127.0.0.1", Port: port}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func connectLoopback(t *testing.T, ep driver.Endpoint, audit func(driver.WriteRecord)) driver.Session {
	t.Helper()
	sess, err := New(Config{Timing: fastTiming}).Connect(context.Background(), ep, driver.Options{WriteAudit: audit})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func runLoopback(t *testing.T, sess driver.Session) <-chan driver.Event {
	t.Helper()
	events := make(chan driver.Event, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sess.Run(ctx, events) }()
	t.Cleanup(func() { cancel(); <-done })
	return events
}

func TestSimLoopback_ConnectSendsNoWrites(t *testing.T) {
	sim, ep := loopback(t, fastSim)
	_ = sim.SurfaceSet("/ch/32/config/name", osc.String("Last"))
	sess := connectLoopback(t, ep, nil)
	st, _ := sess.Snapshot().Strip(model.StripRef{Kind: model.StripInput, Index: 32})
	if st.Name != "Last" {
		t.Fatalf("ch32 name = %q", st.Name)
	}
	if w := sim.Writes(); len(w) != 0 {
		t.Fatalf("Connect sent %d writes over real UDP", len(w))
	}
}

// Both echo variants (X32-F23 documented default and the [UNVERIFIED] echo-to-sender
// variant) must give exactly one write and one echo with the gesture origin.
func TestSimLoopback_EchoOwnWriteDoesNotBounce(t *testing.T) {
	for _, echoToSender := range []bool{false, true} {
		t.Run(map[bool]string{false: "no echo to sender (documented)", true: "echo to sender variant"}[echoToSender], func(t *testing.T) {
			opts := fastSim
			opts.EchoToSender = echoToSender
			sim, ep := loopback(t, opts)
			var mu sync.Mutex
			var audit []driver.WriteRecord
			sess := connectLoopback(t, ep, func(w driver.WriteRecord) { mu.Lock(); audit = append(audit, w); mu.Unlock() })
			events := runLoopback(t, sess)
			v, _ := model.Level(-20)
			g := driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 5}
			if err := sess.Apply(driver.Command{Param: ref(2, model.FieldFader), Value: v, Origin: g}); err != nil {
				t.Fatal(err)
			}
			select {
			case ev := <-events:
				if pc := ev.(driver.ParamChanged); pc.Origin != g {
					t.Fatalf("echo origin = %+v", pc.Origin)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no echo")
			}
			time.Sleep(500 * time.Millisecond)
			writes := sim.Writes()
			mu.Lock()
			defer mu.Unlock()
			if len(writes) != 1 || len(audit) != 1 || writes[0].Address != audit[0].Address || !audit[0].Origin.MayWrite() {
				t.Fatalf("console writes %+v, audit %+v; want one audited gesture write", writes, audit)
			}
		})
	}
}

// X32-F21: with four other /xremote clients the console still answers reads, but
// pushes reach us only if the console evicts the oldest client ([UNVERIFIED] variant).
func TestSimLoopback_FifthClient(t *testing.T) {
	for _, evict := range []bool{false, true} {
		t.Run(map[bool]string{false: "newcomer ignored", true: "oldest evicted"}[evict], func(t *testing.T) {
			opts := fastSim
			opts.EvictOldestWhenFull = evict
			opts.SubscriptionTTL = time.Minute // the four raw clients never renew
			sim, ep := loopback(t, opts)
			for range 4 {
				c, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				pkt, _ := osc.AppendString(nil, "/xremote")
				_, _ = c.WriteTo(pkt, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ep.Port})
			}
			waitFor(t, "four subscribers", func() bool { return len(sim.Subscribers()) == 4 })
			sess := connectLoopback(t, ep, nil)
			events := runLoopback(t, sess)
			_ = sim.SurfaceSet("/ch/01/mix/on", osc.Int(0))
			select {
			case <-events:
				if !evict {
					t.Fatal("a fifth client must get no pushes")
				}
			case <-time.After(500 * time.Millisecond):
				if evict {
					t.Fatal("with eviction the newest client must get pushes")
				}
			}
			if len(sim.Writes()) != 0 {
				t.Fatal("no writes expected")
			}
		})
	}
}
