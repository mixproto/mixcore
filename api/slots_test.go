// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const (
	reconnectRounds = 20
	outage          = 5 * time.Second // longer than the loss detection (X32-F25)
	recoverWait     = 4 * time.Second // backoff max (2 s) plus identify and read
)

// slotUse reports the client ports the simulator saw and the high-water mark
// of its client table.
type slotUse struct {
	ports, xremotePorts, maxSlots int
}

func measureSlots(sim *simx32.Sim) slotUse {
	all, xr := map[netip.AddrPort]bool{}, map[netip.AddrPort]bool{}
	for _, e := range sim.Log() {
		all[e.From] = true
		if e.Address == "/xremote" {
			xr[e.From] = true
		}
	}
	return slotUse{ports: len(all), xremotePorts: len(xr), maxSlots: sim.MaxSlotsUsed()}
}

// X32-F28: the engine keeps its sockets through every reconnect, so 20
// outages cost one console client slot (two under the pessimistic reading
// where any sending port takes a slot), never more. One gesture per round
// uses the write socket too; nothing else is written.
func TestReconnect_TwentyOutagesTakeOneConsoleSlot(t *testing.T) {
	for _, anySender := range []bool{false, true} {
		t.Run(map[bool]string{false: "documented: /xremote ports hold slots", true: "pessimistic: any port holds a slot"}[anySender], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := newEnvWith(t, simx32.Options{Name: "FOH", SlotForAnySender: anySender})
				e.startLive(t)
				for i := range reconnectRounds {
					e.sim.Pause()
					time.Sleep(outage)
					if st := e.rec.last(); st.Status != "stale" {
						t.Fatalf("round %d: status %q during the outage, want stale", i, st.Status)
					}
					e.sim.Resume()
					time.Sleep(recoverWait)
					synctest.Wait()
					if st := e.rec.last(); st.Status != "live" {
						t.Fatalf("round %d: status %q after the outage, want live", i, st.Status)
					}
					if err := e.eng.Apply(cmd("mute", "true", "gesture")); err != nil {
						t.Fatalf("round %d: Apply: %v", i, err)
					}
					synctest.Wait() // the write leaves before the next outage
				}
				synctest.Wait()
				got := measureSlots(e.sim)
				wantSlots := map[bool]int{false: 1, true: 2}[anySender]
				if got.xremotePorts != 1 || got.ports > 2 || got.maxSlots != wantSlots {
					t.Fatalf("after %d reconnects: %+v; want 1 /xremote port, at most 2 ports, %d slot(s)", reconnectRounds, got, wantSlots)
				}
				if w := e.sim.Writes(); len(w) != reconnectRounds {
					t.Fatalf("console received %d writes, want exactly the %d gestures", len(w), reconnectRounds)
				}
			})
		})
	}
}

// staleSinceMs is set once when the console is lost, offline follows 30 s
// later, and both disappear when the console is live again (design 8.1).
func TestStale_StateCarriesStaleSinceAndOffline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		e.sim.Pause()
		time.Sleep(outage)
		st := e.rec.last()
		lost := time.UnixMilli(st.StaleSinceMs)
		if st.Status != "stale" || st.StaleSinceMs == 0 || st.Offline || time.Since(lost) > outage {
			t.Fatalf("during outage: %+v", st)
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if st := e.rec.last(); !st.Offline || st.StaleSinceMs != lost.UnixMilli() {
			t.Fatalf("after 30 s stale: offline %v since %d, want true and %d", st.Offline, st.StaleSinceMs, lost.UnixMilli())
		}
		e.sim.Resume()
		time.Sleep(recoverWait)
		synctest.Wait()
		if st := e.rec.last(); st.Status != "live" || st.Offline || st.StaleSinceMs != 0 {
			t.Fatalf("back live: %+v", st)
		}
	})
}

const holdCh1 = `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":1},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`

// SR-05: going stale (1.5 s without a packet) cancels a held fader at once;
// after the reconnect the old gesture stays ended until a new hold-begin.
func TestStale_EndsHeldFaderWithinOneAndAHalfSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		if err := e.eng.Apply(holdCh1); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second) // meters (50 ms) keep the link busy
		e.sim.Pause()
		time.Sleep(1600 * time.Millisecond)
		synctest.Wait()
		st := e.rec.last()
		if st.Status != "stale" || len(st.Events) != 1 || st.Events[0].Type != "holdCancelled" || st.Events[0].Reason != "stale" {
			t.Fatalf("1.6 s after the cut: %+v", st)
		}
		e.sim.Resume()
		time.Sleep(recoverWait)
		synctest.Wait()
		if err := e.eng.Apply(cmd("fader", "-4", "gesture")); CodeOf(err) != CodeGestureEnded {
			t.Fatalf("Apply of the cancelled gesture = %v, want %s", err, CodeGestureEnded)
		}
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("%d writes after the outage", n)
		}
	})
}
