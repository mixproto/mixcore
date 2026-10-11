// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"errors"
	"math"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// rereadPassTime covers a full pass: one batch at once, then one per ping.
var rereadPassTime = time.Duration((3*inputChannels+2)/rereadBatch+2) * DefaultTiming.PingEvery

func reads(sim *simx32.Sim, addr string) []simx32.Entry {
	var out []simx32.Entry
	for _, e := range sim.Log() {
		if e.Class == simx32.ClassRead && e.Address == addr {
			out = append(out, e)
		}
	}
	return out
}

func drain(events <-chan driver.Event) []driver.Event {
	var out []driver.Event
	for {
		select {
		case ev := <-events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestReread_RecoversLostPushWithoutWriting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		synctest.Wait()

		r.net.SetDropFrom(consoleIP, true)
		_ = r.sim.SurfaceSet("/ch/05/mix/fader", osc.Float(0.25)) // the push is lost on the way
		r.net.SetDropFrom(consoleIP, false)
		synctest.Wait()
		if evs := drain(events); len(evs) != 0 {
			t.Fatalf("the push should have been lost, got %+v", evs)
		}

		if err := sess.(driver.Rereader).Reread(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(rereadPassTime)
		synctest.Wait()
		evs := drain(events)
		if len(evs) != 1 {
			t.Fatalf("re-read reported %d changes, want only the lost one: %+v", len(evs), evs)
		}
		ev := evs[0].(driver.ParamChanged)
		db, _ := ev.Value.DB()
		if ev.Param != ref(5, model.FieldFader) || ev.Origin.Kind != driver.OriginConsole || math.Abs(db+30) > 0.1 {
			t.Fatalf("event = %+v, want ch5 fader -30 dB from the console", ev)
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("a re-read sent %d writes", n) // safety rule 2
		}
	})
}

func TestReread_UnchangedConsoleReportsNothingAndReadsFromTheWriteSocket(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		synctest.Wait()
		_ = sess.(driver.Rereader).Reread()
		_ = sess.(driver.Rereader).Reread() // a running pass is not restarted
		time.Sleep(rereadPassTime)
		synctest.Wait()
		if evs := drain(events); len(evs) != 0 {
			t.Fatalf("unchanged console produced %+v", evs)
		}
		subs := r.sim.Subscribers()
		for _, addr := range []string{"/ch/01/mix/fader", "/ch/32/config/name", "/main/st/mix/on"} {
			got := reads(r.sim, addr)
			if len(got) != 2 {
				t.Fatalf("%s read %d times, want initial read + one re-read", addr, len(got))
			}
			if len(subs) != 1 || got[1].From == subs[0] {
				t.Fatalf("%s re-read from %v, must not come from the /xremote socket %v", addr, got[1].From, subs)
			}
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("a re-read must not write")
		}
	})
}

func TestReread_SkipsParameterWhoseEchoIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		synctest.Wait()

		r.net.SetDropFrom(consoleIP, true) // our write arrives, its echo does not
		if err := sess.Apply(driver.Command{Param: ref(1, model.FieldFader), Value: level(t, -10), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		r.net.SetDropFrom(consoleIP, false)
		_ = sess.(driver.Rereader).Reread()
		time.Sleep(rereadPassTime)
		synctest.Wait()

		if n := len(reads(r.sim, "/ch/01/mix/fader")); n != 1 {
			t.Fatalf("ch1 fader read %d times; a parameter with a pending echo must not be re-read", n)
		}
		if n := len(reads(r.sim, "/ch/01/mix/on")); n != 2 {
			t.Fatalf("ch1 mute read %d times, want 2", n)
		}
		for _, ev := range drain(events) {
			if pc := ev.(driver.ParamChanged); pc.Origin.Kind == driver.OriginConsole {
				t.Fatalf("our own write came back as a console change: %+v", pc)
			}
		}
		r.assertWritesAudited()
	})
}

func TestRereadReply_Bookkeeping(t *testing.T) {
	s := newSession(New(Config{}), nil, driver.Options{})
	s.known = make([]model.Value, len(s.params.all))
	idx := s.params.index(ref(2, model.FieldFader))
	s.known[idx] = mustLevel(t, -20)
	now := time.Now()

	tests := []struct {
		name    string
		asked   bool
		touched bool
		value   model.Value
		want    bool
	}{
		{"late reply of an earlier step is dropped", false, false, mustLevel(t, -5), false},
		{"reply crossed by a write or push is dropped", true, true, mustLevel(t, -5), false},
		{"reply equal to the known value is dropped", true, false, mustLevel(t, -20), false},
		{"reply with a new value is a console change", true, false, mustLevel(t, -5), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clear(s.rr.asked)
			if tc.asked {
				s.rr.asked[idx] = false
				if tc.touched {
					s.markTouched(idx)
				}
			}
			ev, ok := s.rereadReply(idx, tc.value, now)
			if ok != tc.want {
				t.Fatalf("reported = %v, want %v", ok, tc.want)
			}
			if ok && (ev.Origin.Kind != driver.OriginConsole || !s.known[idx].Equal(tc.value)) {
				t.Fatalf("event %+v, known %v", ev, s.known[idx])
			}
		})
	}
}

func TestReread_ClosedSession(t *testing.T) {
	s := newSession(New(Config{}), nil, driver.Options{})
	s.stopped.Store(true)
	if err := s.Reread(); !errors.Is(err, driver.ErrClosed) {
		t.Fatalf("Reread on a closed session = %v", err)
	}
}

// refusingConn fails every send to one address, as an OS refuses a broadcast.
type refusingConn struct {
	net.PacketConn
	refuse netip.AddrPort
}

var errRefused = errors.New("test: send refused")

func (c refusingConn) WriteTo(b []byte, to net.Addr) (int, error) {
	if ua, ok := to.(*net.UDPAddr); ok && ua.AddrPort() == c.refuse {
		return 0, errRefused
	}
	return c.PacketConn.WriteTo(b, to)
}

func TestProbe_RefusedTargetDoesNotStopTheOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{Name: "A"})
		host := r.net.Host(clientIP)
		listen := func(ctx context.Context, network, address string) (net.PacketConn, error) {
			pc, err := host(ctx, network, address)
			return refusingConn{PacketConn: pc, refuse: LimitedBroadcast}, err
		}
		manual := netip.MustParseAddrPort(consoleIP + ":10023")
		found, err := Probe(context.Background(), listen, []netip.AddrPort{LimitedBroadcast, manual}, time.Second)
		if err != nil || len(found) != 1 || found[0].Identity.Name != "A" {
			t.Fatalf("found %+v, err %v; the manual IP must still be probed", found, err)
		}
		_, err = Probe(context.Background(), listen, []netip.AddrPort{LimitedBroadcast}, time.Second)
		if !errors.Is(err, errRefused) {
			t.Fatalf("Probe with every send refused = %v, want the send error", err)
		}
	})
}
