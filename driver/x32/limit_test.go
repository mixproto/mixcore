// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func consoleUDP() net.Addr {
	return net.UDPAddrFromAddrPort(netip.MustParseAddrPort(consoleIP + ":" + fmt.Sprint(DefaultPort)))
}

// fillSlots registers n other /xremote clients and keeps them registered
// until the returned stop is called (X32-F21).
func (r *rig) fillSlots(n int) (stop func()) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := range n {
		pc, err := r.net.ListenPacket(fmt.Sprintf("10.0.5.%d:0", i+1))
		if err != nil {
			r.t.Fatal(err)
		}
		pkt, _ := osc.AppendString(nil, addrXremote)
		to := consoleUDP()
		wg.Go(func() {
			defer pc.Close()
			t := time.NewTicker(3 * time.Second)
			defer t.Stop()
			for {
				_, _ = pc.WriteTo(pkt, to)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		})
	}
	synctest.Wait()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); wg.Wait() }) }
	r.t.Cleanup(stop)
	return stop
}

// nextOf returns the first event of type T within wait.
func nextOf[T driver.Event](events <-chan driver.Event, wait time.Duration) (T, bool) {
	var zero T
	deadline := time.After(wait)
	for {
		select {
		case ev := <-events:
			if e, ok := ev.(T); ok {
				return e, true
			}
		case <-deadline:
			return zero, false
		}
	}
}

func mute(t *testing.T, sess driver.Session, ch int, on bool) {
	t.Helper()
	if err := sess.Apply(driver.Command{Param: ref(ch, model.FieldMute), Value: model.Bool(on), Origin: gesture}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // a separate write, not coalesced
}

// Three value-changing writes without an echo while /xinfo answers: our
// /xremote is not served (X32-F21, X32-F29). A push after a slot frees clears it.
func TestLimit_NoEchoOfOwnWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		stopOthers := r.fillSlots(simx32.DefaultMaxPushClients)
		sess := r.connect()
		events, _ := r.run(sess)
		mute(t, sess, 1, true)
		mute(t, sess, 1, false)
		mute(t, sess, 2, true)
		hit, ok := nextOf[driver.ClientLimitHit](events, DefaultTiming.EchoWindow+time.Second)
		if !ok || hit.Reason != driver.LimitNoEcho || hit.Limit != maxPushClients {
			t.Fatalf("ClientLimitHit = %+v, %v", hit, ok)
		}
		stopOthers()
		time.Sleep(simx32.DefaultSubscriptionTTL + DefaultTiming.RenewEvery) // their slots expire, our renewal gets one
		_ = r.sim.SurfaceSet("/ch/05/mix/on", osc.Int(0))
		if _, ok := nextOf[driver.ClientLimitCleared](events, time.Second); !ok {
			t.Fatal("a push must clear the limit")
		}
	})
}

// A write of the value the console already has may not be pushed (X32-F24):
// it never counts as missing evidence.
func TestLimit_UnchangedWritesDoNotCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.fillSlots(simx32.DefaultMaxPushClients)
		sess := r.connect()
		events, _ := r.run(sess)
		for range 5 {
			mute(t, sess, 1, false) // already unmuted
		}
		if _, ok := nextOf[driver.ClientLimitHit](events, DefaultTiming.EchoWindow+time.Second); ok {
			t.Fatal("unchanged writes must not mark the session limited")
		}
	})
}

// passTime is longer than one paced re-read pass over every parameter.
var passTime = time.Duration(len(newParamTable(inputChannels).all)/rereadBatch+2) * DefaultTiming.PingEvery

// Two re-read passes in a row that find changes no push announced.
func TestLimit_RereadsFindUnpushedChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.fillSlots(simx32.DefaultMaxPushClients)
		sess := r.connect()
		events, _ := r.run(sess)
		rr := sess.(driver.Rereader)
		for i, ch := range []string{"/ch/02/mix/on", "/ch/03/mix/on"} {
			_ = r.sim.SurfaceSet(ch, osc.Int(0)) // an engineer at the desk; we are not told
			if err := rr.Reread(); err != nil {
				t.Fatal(err)
			}
			if _, ok := nextOf[driver.ParamChanged](events, 15*time.Second); !ok {
				t.Fatalf("pass %d did not find the change", i+1)
			}
			time.Sleep(passTime) // the pass ends before the next one starts
		}
		hit, ok := nextOf[driver.ClientLimitHit](events, passTime)
		if !ok || hit.Reason != driver.LimitUnpushedChanges {
			t.Fatalf("ClientLimitHit = %+v, %v", hit, ok)
		}
	})
}

// A served session never reports a limit, however much it writes.
func TestLimit_NeverOnAHealthySession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		for i := range 10 {
			mute(t, sess, 1+i%3, i%2 == 0)
		}
		if _, ok := nextOf[driver.ClientLimitHit](events, 2*DefaultTiming.EchoWindow); ok {
			t.Fatal("limit on a served session")
		}
	})
}
