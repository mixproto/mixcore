// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// fillSlots registers n other apps as /xremote clients, renewing every 3 s.
func (r *rig) fillSlots(n int) (stop func()) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	pcs := make([]interface{ Close() error }, 0, n)
	var subs []func()
	for i := range n {
		pc := r.subscriber(fmt.Sprintf("10.0.1.%d", i+1))
		pcs = append(pcs, pc)
		subs = append(subs, func() { sendTo(r.t, pc, "/xremote") })
	}
	renew := func() {
		for _, s := range subs {
			s()
		}
	}
	renew()
	go func() {
		defer close(done)
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				renew()
			}
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
			for _, pc := range pcs {
				_ = pc.Close()
			}
		})
	}
	r.t.Cleanup(stop)
	return stop
}

// XA-F21: eight other apps hold every push slot; our writes get no echo and
// the session reports the limit of 8. It clears once a slot frees up.
func TestLimit_NinthClientIsLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		stopOthers := r.fillSlots(maxPushClients)
		synctest.Wait()
		sess := r.connect()
		events, _ := r.run(sess)
		for i := 1; i <= 3; i++ { // three value-changing writes (an unchanged value proves nothing, X32-F24)
			if err := sess.Apply(driver.Command{Param: ref(i, model.FieldMute), Value: model.Bool(true), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
		}
		hit, ok := nextOf[driver.ClientLimitHit](events, 5*time.Second)
		if !ok || hit.Limit != 8 || hit.Reason != driver.LimitNoEcho {
			t.Fatalf("ClientLimitHit = %+v, %v", hit, ok)
		}
		stopOthers()                                                          // the other apps leave; their slots expire after 10 s
		time.Sleep(simxair.DefaultSubscriptionTTL + 4*time.Second)            // and our next renewal takes one
		if err := r.sim.SurfaceSet("/ch/09/mix/on", osc.Int(0)); err != nil { // any change is now pushed to us
			t.Fatal(err)
		}
		if _, ok := nextOf[driver.ClientLimitCleared](events, time.Second); !ok {
			t.Fatal("limit must clear once our /xremote is served again")
		}
		r.assertWritesAudited()
	})
}

// A healthy session never reports the limit (eight slots, ours included).
func TestLimit_NeverWithSevenOtherApps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		stop := r.fillSlots(maxPushClients - 1)
		defer stop()
		synctest.Wait()
		sess := r.connect()
		events, _ := r.run(sess)
		for i := 1; i <= 6; i++ {
			if err := sess.Apply(driver.Command{Param: ref(i, model.FieldMute), Value: model.Bool(true), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
		}
		if ev, ok := nextOf[driver.ClientLimitHit](events, 40*time.Second); ok {
			t.Fatalf("limit reported on a served session: %+v", ev)
		}
	})
}

func burstScene(n int) map[string]osc.Arg {
	out := map[string]osc.Arg{}
	for ch := 1; ch <= n; ch++ {
		out[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.25)
	}
	return out
}

// XA section 7: confidence from the burst and the /-snap/index push, which
// names the loaded snapshot. The driver never writes a /-snap/ address.
func TestSceneRecall_ConfidenceAndLoadedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pushIdx  bool
		params   int
		conf     string
		signals  []string
		loaded   int
		noReport bool
	}{
		{"burst and index", true, 10, driver.ConfidenceHigh, []string{driver.SignalBurst, driver.SignalSnapshotIndex}, 12, false},
		{"index alone", true, 2, driver.ConfidenceMedium, []string{driver.SignalSnapshotIndex}, 12, false},
		{"burst alone (index not pushed)", false, 10, driver.ConfidenceLow, []string{driver.SignalBurst}, driver.NoScene, false},
		{"neither", false, 2, "", nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, simxair.Options{PushSnapIndex: tc.pushIdx})
				if err := r.sim.StoreSnapshot(12, "Band", burstScene(tc.params)); err != nil {
					t.Fatal(err)
				}
				sess := r.connect()
				events, _ := r.run(sess)
				if err := r.sim.LoadSnapshot(12); err != nil {
					t.Fatal(err)
				}
				ev, ok := nextOf[driver.SceneRecalled](events, 2*time.Second)
				if tc.noReport {
					if ok {
						t.Fatalf("unexpected recall %+v", ev)
					}
					return
				}
				if !ok || ev.Confidence != tc.conf || !slices.Equal(ev.Signals, tc.signals) || ev.LoadedScene != tc.loaded || ev.HighlightedScene != driver.NoScene {
					t.Fatalf("SceneRecalled = %+v, %v", ev, ok)
				}
				time.Sleep(10 * time.Second) // the re-read after the recall runs and writes nothing
				for _, e := range r.sim.Log() {
					if strings.HasPrefix(e.Address, "/-snap/") {
						t.Fatalf("the driver sent %s %s; it must never touch snapshots", e.Class, e.Address)
					}
				}
				if n := len(r.sim.Writes()); n != 0 {
					t.Fatalf("%d writes around a recall", n)
				}
			})
		})
	}
}

// A malformed /-snap/index push (out of 1..64) is dropped, never guessed.
func TestSceneRecall_MalformedIndexIsNotASignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		for _, a := range []osc.Arg{osc.Int(65), osc.Int(0), osc.String("12"), osc.Float(12)} {
			if err := r.sim.InjectPush(addrSnapIndex, a); err != nil {
				t.Fatal(err)
			}
		}
		if ev, ok := nextOf[driver.SceneRecalled](events, 2*time.Second); ok {
			t.Fatalf("recall from a malformed index: %+v", ev)
		}
	})
}

// XA-F72, test item 12: a small model that does not answer channels 13..16
// connects with 12 inputs; mute groups and DCAs stay.
func TestConnect_SmallModelWithoutUpperInputs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{Model: "XR12", InputChannels: 12, Buses: 2})
		sess := r.connect()
		c := sess.Capabilities()
		if c.InputCount != 12 || c.MuteGroups != 4 || c.DCAs != 4 || len(sess.Snapshot().Inputs()) != 12 {
			t.Fatalf("capabilities %+v, inputs %d", c, len(sess.Snapshot().Inputs()))
		}
		err := sess.Apply(driver.Command{Param: ref(13, model.FieldMute), Value: model.Bool(true), Origin: gesture})
		if err == nil {
			t.Fatal("input 13 is absent; a write must be refused")
		}
		events, _ := r.run(sess)
		if err := sess.(driver.Rereader).Reread(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(8 * time.Second)
		for _, ev := range drain(events) {
			if pc, ok := ev.(driver.ParamChanged); ok && pc.Param.Strip.Index > 12 && pc.Param.Strip.Kind == model.StripInput {
				t.Fatalf("event for an absent strip: %+v", pc)
			}
		}
	})
}

// With only channel 16 missing the session has 15 inputs.
func TestConnect_MissingChannel16(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{InputChannels: 15})
		if n := r.connect().Capabilities().InputCount; n != 15 {
			t.Fatalf("inputs %d, want 15", n)
		}
	})
}

// Rule 3 over a link: a reconnect replaces state with the mixer's, queued
// writes are dropped, the source ports stay the same (X32-F28 holds by XA-F01).
func TestLink_ReconnectReadsTheMixerAndKeepsPorts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		l, err := r.drv.OpenLink(context.Background(), driver.Endpoint{Host: mixerIP}, r.opts())
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		sess, err := l.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		before := sourcePorts(r)
		r.net.SetDown(mixerIP, true)
		_ = sess.Apply(driver.Command{Param: ref(1, model.FieldMute), Value: model.Bool(true), Origin: gesture})
		_ = sess.Close() // the supervisor closes a lost session: queued writes are dropped
		if err := r.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.5)); err != nil {
			t.Fatal(err)
		}
		r.net.SetDown(mixerIP, false)
		sess, err = l.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		in1, _ := sess.Snapshot().Strip(model.StripRef{Kind: model.StripInput, Index: 1})
		if in1.Muted || in1.FaderDB > -9 || in1.FaderDB < -11 {
			t.Fatalf("after reconnect input 1 = %+v, want the mixer's state", in1)
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("%d writes replayed after a reconnect", n)
		}
		if after := sourcePorts(r); !slices.Equal(before, after) {
			t.Fatalf("source ports %v then %v", before, after)
		}
	})
}

func sourcePorts(r *rig) []string {
	seen := map[string]bool{}
	for _, e := range r.sim.Log() {
		seen[e.From.String()] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
