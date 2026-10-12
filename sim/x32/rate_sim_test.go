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

	"github.com/mixproto/mixcore/internal/osc"
)

// On real timers, which jitter, a meter stream keeps its 50 ms interval
// (X32-F50): 20 packets per second, not one skipped tick in two or three.
func TestSim_MeterRateOnRealTimers(t *testing.T) {
	const (
		window  = 2 * time.Second
		want    = int(window / DefaultMeterInterval)
		atLeast = want * 85 / 100 // a loaded CI runner may lose a few ticks
	)
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	defer func() { cancel(); wg.Wait(); _ = pc.Close() }()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pkt, _ := osc.AppendString(nil, "/meters", osc.String("/meters/1"))
	if _, err := c.WriteTo(pkt, pc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n := 0
	end := time.Now().Add(window)
	for time.Now().Before(end) {
		_ = c.SetReadDeadline(end)
		if _, _, err := c.ReadFrom(buf); err == nil {
			n++
		}
	}
	t.Logf("%d meter packets in %s, %d expected", n, window, want)
	if n < atLeast || n > want+1 {
		t.Fatalf("%d meter packets in %s, want about %d (X32-F50)", n, window, want)
	}
}
