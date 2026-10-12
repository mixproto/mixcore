// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package xair

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Scaled timing: driver and simulator get the same 10x shorter lifetimes.
var (
	fastTiming = Timing{
		RenewEvery: 300 * time.Millisecond, PingEvery: 100 * time.Millisecond, DeadAfter: 400 * time.Millisecond,
		ConnectTimeout: 3 * time.Second, ReadRetryEvery: 100 * time.Millisecond, EchoWindow: time.Second,
	}
	fastSim = simxair.Options{SubscriptionTTL: time.Second, MeterTTL: time.Second}
)

// loopback serves a simulator on a real UDP socket on 127.0.0.1.
func loopback(t *testing.T, opts simxair.Options) (*simxair.Sim, driver.Endpoint) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simxair.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	return sim, driver.Endpoint{Host: "127.0.0.1", Port: pc.LocalAddr().(*net.UDPAddr).Port}
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

func connectLoopback(t *testing.T, ep driver.Endpoint) driver.Session {
	t.Helper()
	sess, err := New(Config{Timing: fastTiming}).Connect(context.Background(), ep, driver.Options{})
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
	sess := connectLoopback(t, ep)
	if sess.Capabilities().InputCount != inputChannels || len(sim.Writes()) != 0 {
		t.Fatalf("caps %+v, writes %d", sess.Capabilities(), len(sim.Writes()))
	}
}

// Over real UDP, under the default echo rule and H3, our write is echoed once
// with our origin and never comes back as a mixer change.
func TestSimLoopback_EchoOwnWriteDoesNotBounce(t *testing.T) {
	for name, extra := range map[string]simxair.Options{"default": {}, "H3 reply to a set": {ReplyToSet: true}} {
		t.Run(name, func(t *testing.T) {
			opts := fastSim
			opts.ReplyToSet = extra.ReplyToSet
			sim, ep := loopback(t, opts)
			sess := connectLoopback(t, ep)
			events := runLoopback(t, sess)
			v, _ := model.Level(-20)
			g := driver.Origin{Kind: driver.OriginGesture, ClientID: "loop", Seq: 9}
			if err := sess.Apply(driver.Command{Param: ref(4, model.FieldFader), Value: v, Origin: g}); err != nil {
				t.Fatal(err)
			}
			deadline := time.After(time.Second)
			var mine, other int
			for done := false; !done; {
				select {
				case ev := <-events:
					if pc, ok := ev.(driver.ParamChanged); ok && pc.Param == ref(4, model.FieldFader) {
						if pc.Origin == g {
							mine++
						} else {
							other++
						}
					}
				case <-deadline:
					done = true
				}
			}
			if mine != 1 || other != 0 || len(sim.Writes()) != 1 {
				t.Fatalf("echo %d times with our origin, %d otherwise; %d writes", mine, other, len(sim.Writes()))
			}
		})
	}
}

// XA-F21: with eight other apps registered, the ninth client gets no pushes
// by default; with the evict option it gets them and the oldest loses.
func TestSimLoopback_NinthClient(t *testing.T) {
	for _, evict := range []bool{false, true} {
		t.Run(map[bool]string{false: "newcomer ignored", true: "oldest evicted"}[evict], func(t *testing.T) {
			opts := fastSim
			opts.EvictOldestWhenFull = evict
			opts.SubscriptionTTL = time.Minute // the eight raw clients never renew
			sim, ep := loopback(t, opts)
			for range maxPushClients {
				c, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				pkt, _ := osc.AppendString(nil, "/xremote")
				_, _ = c.WriteTo(pkt, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ep.Port})
			}
			waitFor(t, "eight subscribers", func() bool { return len(sim.Subscribers()) == maxPushClients })
			sess := connectLoopback(t, ep)
			events := runLoopback(t, sess)
			_ = sim.SurfaceSet("/ch/01/mix/on", osc.Int(0))
			select {
			case <-events:
				if !evict {
					t.Fatal("a ninth client must get no pushes")
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
