// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"bytes"
	"context"
	"math"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// reconnectTarget is the design target for input unlocked after the
// network returns (go-core-standards section 4, CLAUDE.md north star).
const reconnectTarget = 2 * time.Second

// lossyConn drops the first n sends that start with prefix while armed.
type lossyConn struct {
	net.PacketConn
	mu      *sync.Mutex
	armed   *bool
	prefix  []byte
	dropped *int
	n       int
}

func (c lossyConn) WriteTo(b []byte, to net.Addr) (int, error) {
	c.mu.Lock()
	drop := *c.armed && bytes.HasPrefix(b, c.prefix) && *c.dropped < c.n
	if drop {
		*c.dropped++
	}
	c.mu.Unlock()
	if drop {
		return len(b), nil // lost on the way, as UDP does
	}
	return c.PacketConn.WriteTo(b, to)
}

func (r *rig) openLink(t *testing.T, opts driver.Options) driver.Link {
	t.Helper()
	l, err := r.drv.OpenLink(context.Background(), driver.Endpoint{Host: mixerIP}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func timedConnect(t *testing.T, l driver.Link) (driver.Session, time.Duration) {
	t.Helper()
	t0 := time.Now()
	sess, err := l.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sess, time.Since(t0)
}

// XA-F72: on a 12-input mixer the first connect probes the optional strips
// once and stays under the reconnect target; every reconnect over the same
// link knows the strip count and does not wait for dead reads at all.
func TestLink_TwelveInputMixerConnectsFast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{InputChannels: 12})
		l := r.openLink(t, r.opts())
		sess, first := timedConnect(t, l)
		if n := sess.Capabilities().InputCount; n != 12 || first > reconnectTarget {
			t.Fatalf("first connect: %d inputs in %s", n, first)
		}
		_ = sess.Close()
		sess, again := timedConnect(t, l)
		defer sess.Close()
		if n := sess.Capabilities().InputCount; n != 12 || again > 100*time.Millisecond {
			t.Fatalf("reconnect: %d inputs in %s, want 12 at once", n, again)
		}
	})
}

// A strip that answered once is required on every later reconnect: packet
// loss during a reconnect delays it but never drops real strips.
func TestLink_LossyReconnectKeepsStripsThatAnswered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		var mu sync.Mutex
		var armed bool
		dropped := 0
		opts := r.opts()
		host := opts.ListenPacket
		opts.ListenPacket = func(ctx context.Context, network, address string) (net.PacketConn, error) {
			pc, err := host(ctx, network, address)
			return lossyConn{PacketConn: pc, mu: &mu, armed: &armed, prefix: []byte("/ch/14/mix/fader"), dropped: &dropped, n: 6}, err
		}
		l := r.openLink(t, opts)
		sess, _ := timedConnect(t, l)
		if n := sess.Capabilities().InputCount; n != 16 {
			t.Fatalf("first connect: %d inputs", n)
		}
		_ = sess.Close()
		mu.Lock()
		armed = true
		mu.Unlock()
		sess, _ = timedConnect(t, l)
		defer sess.Close()
		if n := sess.Capabilities().InputCount; n != 16 || len(sess.Snapshot().Inputs()) != 16 {
			t.Fatalf("lossy reconnect kept %d inputs, want 16 (dropped %d reads)", n, dropped)
		}
	})
}

// The strips that did not answer are surfaced in the capabilities.
func TestConnect_AbsentInputsAreReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{InputChannels: 13})
		c := r.connect().Capabilities()
		if c.InputCount != 13 || c.AbsentInputs != 3 {
			t.Fatalf("capabilities %+v", c)
		}
	})
}

// The frame keeps n = 18 (Main L/R at n-2, n-1) on a small model; the inputs
// the mixer did not answer read -inf whatever bank 1 carries there.
func TestMeters_AbsentInputsReadMinusInf(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{InputChannels: 12})
		vals := make([]int16, 16)
		for i := range vals {
			vals[i] = -256 * int16(i+1) // -1 .. -16 dBFS
		}
		r.sim.SetMeterValues(vals)
		r.sim.SetMeterValue(simxair.Bank1MainL, -512)
		r.sim.SetMeterValue(simxair.Bank1MainL+1, -768)
		sess := r.connect()
		r.run(sess)
		if err := sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		var fr meters.Frame
		if !r.slot.CopyLatest(&fr) || fr.Count != meterLevels {
			t.Fatalf("frame count %d", fr.Count)
		}
		for i := range 12 {
			if fr.Levels[i] != float32(-(i + 1)) {
				t.Fatalf("input %d = %v", i+1, fr.Levels[i])
			}
		}
		for i := 12; i < 16; i++ {
			if !math.IsInf(float64(fr.Levels[i]), -1) {
				t.Fatalf("absent input %d = %v, want -inf", i+1, fr.Levels[i])
			}
		}
		if fr.Levels[mainLLevel] != -2 || fr.Levels[mainLLevel+1] != -3 {
			t.Fatalf("Main L/R = %v %v", fr.Levels[mainLLevel], fr.Levels[mainLLevel+1])
		}
	})
}
