// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/memnet"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

func (r *rig) link(t *testing.T) *link {
	t.Helper()
	l, err := r.drv.OpenLink(t.Context(), driver.Endpoint{Host: consoleIP}, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.(*link)
}

// redial calls Connect every 50 ms until the link dials again and returns
// how long it held off; every refusal in between must be a mismatch.
func redial(t *testing.T, r *rig, l *link) time.Duration {
	t.Helper()
	start, dials := time.Now(), r.dials
	for {
		_, err := l.Connect(t.Context())
		if r.dials > dials {
			return time.Since(start)
		}
		if !errors.Is(err, ErrProtocolMismatch) || !errors.Is(err, driver.ErrProtocolMismatch) {
			t.Fatalf("held-off connect: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestProtocolMismatch_BacksOffNeverLatches (security re-check 2): schema
// failures hold dialing off for 1, 2, 4 ... 30 s, and never for good.
func TestProtocolMismatch_BacksOffNeverLatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{UnknownPaths: []string{"/ch/1/fdr"}})
		l := r.link(t)
		want := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
		for i, w := range want {
			if got := redial(t, r, l); got < w || got > w+100*time.Millisecond {
				t.Fatalf("attempt %d held off %v, want %v", i+1, got, w)
			}
		}
	})
}

// TestProtocolMismatch_FloodNeverHolds: a definition flood (errWalkLimit)
// is not a protocol mismatch: every attempt dials.
func TestProtocolMismatch_FloodNeverHolds(t *testing.T) {
	var sent atomic.Int64
	var wg sync.WaitGroup
	dials := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		dials++
		client, server := net.Pipe()
		wg.Go(func() { floodConsole(server, smallDef, &sent); _ = server.Close() })
		return client, nil
	}
	l, err := New(Config{}).OpenLink(context.Background(), driver.Endpoint{Host: consoleIP},
		driver.Options{Dial: dial, ListenPacket: memnet.New().Host(clientIP)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for range 4 {
		if _, err := l.Connect(context.Background()); !errors.Is(err, errWalkLimit) {
			t.Fatalf("flood: %v", err)
		}
	}
	wg.Wait()
	if dials != 4 {
		t.Fatalf("%d dials for 4 attempts: a flood must not hold dialing off", dials)
	}
}

// TestProtocolMismatch_EarlyStreamDeathCounts (go-reviewer L1): a session
// that dies of undefined bytes soon after connect counts like a schema
// failure; one that dies so after a healthy run does not.
func TestProtocolMismatch_EarlyStreamDeathCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		l := r.link(t)
		for _, tc := range []struct {
			after time.Duration
			held  bool
		}{{time.Second, true}, {20 * time.Second, false}} {
			s, err := l.Connect(t.Context())
			if err != nil {
				t.Fatalf("after %v: %v", tc.after, err)
			}
			rn := r.run(s.(*session))
			time.Sleep(tc.after)
			r.sim.SendRaw([]byte{0xDF, 0xD1, 0xE5})
			if err := <-rn.done; !errors.Is(err, errStream) {
				t.Fatalf("junk: %v", err)
			}
			rn.done <- nil
			dials := r.dials
			_, err = l.Connect(t.Context())
			held := r.dials == dials
			if held != tc.held || (held && !errors.Is(err, ErrProtocolMismatch)) {
				t.Fatalf("death %v after connect: held %v, %v", tc.after, held, err)
			}
			if held {
				time.Sleep(2 * time.Second) // past the hold-off
			}
		}
	})
}

// TestProtocolMismatch_SupervisorRecovers (security re-check 2): three bad
// walks, then the real console: live within the backoff window, and the
// code reached the update meanwhile.
func TestProtocolMismatch_SupervisorRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		bad := simwing.New(simwing.Options{UnknownPaths: []string{"/ch/1/fdr"}})
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		defer func() { cancel(); bad.DropConnections(); wg.Wait() }()
		opts, good := r.opts(), r.opts().Dial
		badDials := 0
		opts.Dial = func(c context.Context, n, a string) (net.Conn, error) {
			if badDials < 3 {
				badDials++
				client, server := net.Pipe()
				wg.Go(func() { bad.ServeConn(ctx, server) })
				return client, nil
			}
			return good(c, n, a)
		}
		sup, err := console.New(console.Config{Driver: r.drv, Endpoint: driver.Endpoint{Host: consoleIP}, Options: opts, RereadEvery: -1})
		if err != nil {
			t.Fatal(err)
		}
		if err := sup.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer sup.Stop()
		sawCode := false
		start := time.Now()
		for sup.Snapshot().Status != console.StatusLive {
			if time.Since(start) > 12*time.Second {
				t.Fatalf("not live %v after three bad walks", time.Since(start))
			}
			sawCode = sawCode || errors.Is(sup.Snapshot().Cause, driver.ErrProtocolMismatch)
			time.Sleep(50 * time.Millisecond)
		}
		if !sawCode || badDials != 3 {
			t.Fatalf("mismatch reported %v, bad walks %d", sawCode, badDials)
		}
	})
}
