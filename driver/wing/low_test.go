// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestReads_ExpireUnanswered (review go L2): a read the console never
// answered is forgotten after readExpiry, so a later push of that
// parameter is a push, not a stale reply.
func TestReads_ExpireUnanswered(t *testing.T) {
	s := &session{}
	now := time.Unix(1000, 0)
	s.own.reads = map[int][]readEntry{1: {{at: now.Add(-3 * time.Second), settle: true}}, 2: {{at: now.Add(-time.Second)}}}
	s.expireReads(now)
	if _, ok := s.own.reads[1]; ok {
		t.Fatal("a read unanswered for 3 s is still pending")
	}
	if _, ok := s.own.reads[2]; !ok {
		t.Fatal("a read 1 s old was dropped")
	}
}

// TestSameFader_BelowDisplayRange (review go L3): below -90 dB faders are
// compared in dB, so -95 dB and -inf are different values.
func TestSameFader_BelowDisplayRange(t *testing.T) {
	inf := math.Inf(-1)
	cases := []struct {
		a, b float64
		want bool
	}{{-95, inf, false}, {-95, -95.01, true}, {inf, inf, true}, {-95, -100, false}, {-3, -3.01, true}, {0, 0.5, false}}
	for _, tc := range cases {
		if got := sameFader(tc.a, tc.b); got != tc.want {
			t.Errorf("sameFader(%v, %v) = %v", tc.a, tc.b, got)
		}
	}
}

// TestHeldFader_DeskToInfFromBelow90EndsHold (review go L3): we wrote -95
// dB; the desk pulls it to -inf before any read-back: a console change.
func TestHeldFader_DeskToInfFromBelow90EndsHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		_ = rn.s.Apply(driver.Command{Param: ch1Fader, Value: level(t, -95), Origin: gesture})
		time.Sleep(50 * time.Millisecond)
		mustSet(t, r.sim, "/ch/1/fdr", -144.0)
		for _, c := range rn.changes(time.Second) {
			if db, _ := c.Value.DB(); c.Param == ch1Fader && math.IsInf(db, -1) && (c.Origin.Kind != driver.OriginConsole || c.OwnWrite) {
				t.Fatalf("the desk's -inf taken as our -95 dB: %+v", c)
			}
		}
	})
}

// TestReconnect_NoReplay (review go L4): a write sent while the console is
// stalled is never sent again after the connection is lost and reopened.
func TestReconnect_NoReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		l := r.link(t)
		sess, err := l.Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		rn := r.run(sess.(*session))
		r.sim.Pause()
		if err := sess.Apply(driver.Command{Param: ch1Fader, Value: level(t, -12), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		r.sim.DropConnections()
		r.sim.Resume()
		<-rn.done
		rn.done <- nil
		sess2, err := l.Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		r.run(sess2.(*session))
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("a write reached the console after the reconnect: %+v", w)
		}
		if a := r.audits(); len(a) != 1 {
			t.Fatalf("the write was sent %d times", len(a))
		}
	})
}

// TestSettle_DeskPushDuringReadBack (review go L5): a desk change that
// crosses our read-back is reported as the console's, ends the hold, and
// is the final value, in either arrival order.
func TestSettle_DeskPushDuringReadBack(t *testing.T) {
	for _, order := range []string{"push first", "reply first"} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, simwing.Options{})
			s := r.connect()
			if err := s.Apply(driver.Command{Param: ch1Fader, Value: level(t, -10), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			if err := s.flushPending(); err != nil {
				t.Fatal(err)
			}
			s.own.settleAt[s.tab.byRef[ch1Fader]] = time.Now()
			if err := s.sendSettleReads(time.Now()); err != nil {
				t.Fatal(err)
			}
			hash := s.tab.all[s.tab.byRef[ch1Fader]].hash
			desk := inMsg{kind: msgValue, hash: hash, val: wireValue{kind: wireFloat, f: -20}, at: time.Now()}
			reply := inMsg{kind: msgValue, hash: hash, val: wireValue{kind: wireFloat, f: -10}, at: time.Now()}
			if order == "push first" {
				reply.val.f = -20 // the console applied the desk move before our read
				s.onMessage(desk)
				s.onMessage(reply)
			} else {
				s.onMessage(reply)
				s.onMessage(desk)
			}
			var external, last *driver.ParamChanged
			for _, ev := range s.own.outbox {
				if c, ok := ev.(driver.ParamChanged); ok && c.Param == ch1Fader {
					if db, _ := c.Value.DB(); db == -20 && c.Origin.Kind == driver.OriginConsole && !c.OwnWrite {
						external = &c
					}
					last = &c
				}
			}
			if external == nil || last == nil {
				t.Fatalf("%s: no external change of -20 dB: %+v", order, s.own.outbox)
			}
			if db, _ := last.Value.DB(); db != -20 {
				t.Fatalf("%s: final value %v dB", order, db)
			}
		})
	}
}

// TestMeterID_Random (review sec L5): report ids carry 16 random bits, not
// a session counter another host could guess.
func TestMeterID_Random(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		l := r.link(t)
		sequential := true
		for i := 1; i <= 6; i++ {
			s, err := l.Connect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			id := s.(*session).meterID
			if id&0xFFFF0000 != meterIDTag {
				t.Fatalf("id %#x without the tag", id)
			}
			sequential = sequential && id&0xFFFF == uint32(i)
			_ = s.Close()
		}
		if sequential {
			t.Fatal("report ids follow the session counter")
		}
	})
}

