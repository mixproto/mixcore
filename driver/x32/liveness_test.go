// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// staleTolerance is how late the stale decision may come: one liveness tick.
const staleTolerance = 100 * time.Millisecond

// X32-F25: with meters flowing (a packet every 50 ms) the session is lost
// 1.5 s after the last packet, not after the 3 s /xinfo timeout.
func TestLiveness_LostAfterOneAndAHalfSecondsWithoutAnyPacket(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		done := runDone(t, sess)
		_ = sess.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(2 * time.Second)
		r.sim.Pause()
		cut := time.Now()
		err := <-done
		took := time.Since(cut)
		if !errors.Is(err, driver.ErrConnectionLost) {
			t.Fatalf("Run err = %v, want ErrConnectionLost", err)
		}
		if took < DefaultTiming.StaleAfter-meterGap || took > DefaultTiming.StaleAfter+staleTolerance {
			t.Fatalf("lost %s after the cut, want %s (+%s)", took, DefaultTiming.StaleAfter, staleTolerance)
		}
	})
}

// meterGap is the documented meter interval (X32-F50): the last packet before
// a cut arrived at most this long before it.
const meterGap = 50 * time.Millisecond

// runDone runs sess until the test ends and returns a channel with Run's error.
func runDone(t *testing.T, sess driver.Session) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	events := make(chan driver.Event, 256)
	go func() { done <- sess.Run(ctx, events) }()
	t.Cleanup(func() { cancel(); _ = sess.Close() })
	return done
}

// A quiet console (no meters, nothing changes) answers the quiet /xinfo
// probes, so it never goes stale, and the probes stay few.
func TestLiveness_QuietConsoleStaysLiveWithFewProbes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		_, stop := r.run(sess)
		before := countXinfo(r.sim)
		time.Sleep(10 * time.Minute)
		if err := stop(); err != nil {
			t.Fatalf("a quiet console went stale: %v", err)
		}
		perSecond := float64(countXinfo(r.sim)-before) / (10 * 60)
		if perSecond > 1/DefaultTiming.QuietProbeAfter.Seconds()+0.1 {
			t.Fatalf("%.2f /xinfo probes per second, want at most one per %s", perSecond, DefaultTiming.QuietProbeAfter)
		}
	})
}

// With meters flowing the quiet probe stays silent; only the uplink check
// (one /xinfo per PingEvery) is sent.
func TestLiveness_NoQuietProbesWhileMetersFlow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		r.run(sess)
		_ = sess.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(time.Second)
		before := countXinfo(r.sim)
		time.Sleep(time.Minute)
		if n := countXinfo(r.sim) - before; n > int(time.Minute/DefaultTiming.PingEvery)+1 {
			t.Fatalf("%d /xinfo in a minute with meters on, want about %d", n, time.Minute/DefaultTiming.PingEvery)
		}
	})
}

func TestTiming_ZeroFieldsTakeDefaults(t *testing.T) {
	tm := Timing{RenewEvery: time.Second}.WithDefaults()
	if tm.RenewEvery != time.Second || tm.StaleAfter != DefaultTiming.StaleAfter || tm.LivenessTick != DefaultTiming.LivenessTick {
		t.Fatalf("timing = %+v", tm)
	}
}

func countXinfo(sim *simx32.Sim) int {
	n := 0
	for _, e := range sim.Log() {
		if e.Address == "/xinfo" {
			n++
		}
	}
	return n
}
