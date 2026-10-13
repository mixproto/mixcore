// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc_test

import (
	"context"
	"math"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/driver/xair"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// The shared engine of core-api v0.5 (settle re-read, bus meters), run
// through both family profiles against their simulators on the in-memory
// network: the behaviour holds for x32 and xair alike.

const (
	consoleIP    = "10.0.0.1"
	clientIP     = "10.0.0.2"
	settleBudget = 250 * time.Millisecond // redesign CC-3
)

// family is one profile with its simulator and the facts the tests need.
type family struct {
	name     string
	port     string
	drv      driver.Driver
	gainAddr string // headamp 1's Gain on the wire
	busFirst int    // level of bus 1's meter in the frame
	buses    int
	serve    func(t *testing.T, pc net.PacketConn, noGainPush bool) func(bus2 float64) // returns: set bus 2's meter (dBFS)
}

func families() []family {
	return []family{
		{name: "x32", port: "10023", drv: x32.New(x32.Config{}), gainAddr: "/headamp/000/gain", busFirst: 32, buses: 16,
			serve: func(t *testing.T, pc net.PacketConn, noEcho bool) func(float64) {
				sim := simx32.New(simx32.Options{NoGainPush: noEcho})
				serveSim(t, func(ctx context.Context) { _ = sim.Serve(ctx, pc) })
				return func(db float64) { sim.SetBusLevels([]float32{0, float32(math.Pow(10, db/20))}) }
			}},
		{name: "xair", port: "10024", drv: xair.New(xair.Config{}), gainAddr: "/headamp/01/gain", busFirst: 16, buses: 10,
			serve: func(t *testing.T, pc net.PacketConn, noEcho bool) func(float64) {
				sim := simxair.New(simxair.Options{NoGainPush: noEcho})
				serveSim(t, func(ctx context.Context) { _ = sim.Serve(ctx, pc) })
				return func(db float64) { sim.SetMeterValue(27, int16(db*256)) } // bank 1 value 27: bus 2 (XA-F53)
			}},
	}
}

func serveSim(t *testing.T, serve func(context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { serve(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
}

type rig struct {
	net    *memnet.Network
	sess   driver.Session
	events chan driver.Event
	slot   *meters.Slot
	bus2   func(db float64)
}

// connect starts the family's simulator and a running session to it.
func connect(t *testing.T, f family, noGainPush bool) *rig {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":" + f.port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	r := &rig{net: n, slot: &meters.Slot{}, events: make(chan driver.Event, 512)}
	r.bus2 = f.serve(t, pc, noGainPush)
	r.sess, err = f.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, driver.Options{ListenPacket: n.Host(clientIP), Meters: r.slot})
	if err != nil {
		t.Fatalf("%s: connect: %v", f.name, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = r.sess.Run(ctx, r.events) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = r.sess.Close() })
	return r
}

var gesture = driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 1}

func gain1() model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: 1}, Field: model.FieldGain}
}

func writeGain(t *testing.T, r *rig, db float64) {
	t.Helper()
	v, _ := model.Level(db)
	if err := r.sess.Apply(driver.Command{Param: gain1(), Value: v, Origin: gesture}); err != nil {
		t.Fatal(err)
	}
}

// nextGainReport returns the next report of headamp 1's Gain within wait.
func nextGainReport(r *rig, wait time.Duration) (driver.ParamChanged, bool) {
	deadline := time.After(wait)
	for {
		select {
		case ev := <-r.events:
			if pc, ok := ev.(driver.ParamChanged); ok && pc.Param == gain1() {
				return pc, true
			}
		case <-deadline:
			return driver.ParamChanged{}, false
		}
	}
}

// CC-3: a burst of Gain writes the console does not echo is read back once,
// within 250 ms of the last write, as our own authoritative value.
func TestEngine_SettleReadBackInEveryFamily(t *testing.T) {
	for _, f := range families() {
		t.Run(f.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := connect(t, f, true)
				for _, db := range []float64{3, 4, 5} {
					writeGain(t, r, db)
					time.Sleep(25 * time.Millisecond)
				}
				last := time.Now().Add(-25 * time.Millisecond)
				ev, ok := nextGainReport(r, settleBudget)
				if !ok || time.Since(last) > settleBudget {
					t.Fatalf("no read-back within 250 ms (%v)", time.Since(last))
				}
				want, _ := model.Level(5)
				if !ev.Authoritative || !ev.OwnWrite || !ev.Value.Equal(want) {
					t.Fatalf("read-back %+v", ev)
				}
				if _, again := nextGainReport(r, time.Second); again {
					t.Fatal("a second read-back for one burst")
				}
			})
		})
	}
}

// CC-3: a write the console echoes is never read back.
func TestEngine_EchoedGainIsNotReadBackInEveryFamily(t *testing.T) {
	for _, f := range families() {
		t.Run(f.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := connect(t, f, false)
				writeGain(t, r, 6)
				ev, ok := nextGainReport(r, settleBudget)
				if !ok || ev.Authoritative || ev.Origin.ClientID != gesture.ClientID {
					t.Fatalf("want our echo, got %+v %v", ev, ok)
				}
				if ev, again := nextGainReport(r, time.Second); again {
					t.Fatalf("a read-back of an echoed write: %+v", ev)
				}
			})
		})
	}
}

// CC-3: a read-back whose reply is lost reports nothing; the next write's
// read-back reports again.
func TestEngine_LostReadBackReplyInEveryFamily(t *testing.T) {
	for _, f := range families() {
		t.Run(f.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := connect(t, f, true)
				writeGain(t, r, 2)
				synctest.Wait()
				r.net.SetDropFrom(consoleIP, true) // the console's replies are lost for a while
				time.Sleep(300 * time.Millisecond)
				r.net.SetDropFrom(consoleIP, false)
				if ev, ok := nextGainReport(r, 10*time.Millisecond); ok {
					t.Fatalf("a lost reply reported %+v", ev)
				}
				writeGain(t, r, 7)
				ev, ok := nextGainReport(r, settleBudget)
				want, _ := model.Level(7)
				if !ok || !ev.Authoritative || !ev.Value.Equal(want) {
					t.Fatalf("read-back after a lost reply: %+v %v", ev, ok)
				}
			})
		})
	}
}

// CC-13: the capabilities say where the bus meters are, and bus 2's meter
// arrives there, before Main L/R.
func TestEngine_BusMetersInEveryFamily(t *testing.T) {
	for _, f := range families() {
		t.Run(f.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := connect(t, f, false)
				if c := r.sess.Capabilities(); c.BusMeterFirst != f.busFirst || c.BusMeters != f.buses {
					t.Fatalf("bus meters %d from %d, want %d from %d", c.BusMeters, c.BusMeterFirst, f.buses, f.busFirst)
				}
				r.bus2(-6)
				if err := r.sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second)
				var fr meters.Frame
				if !r.slot.CopyLatest(&fr) || fr.Count != f.busFirst+f.buses+2 || math.Abs(float64(fr.Levels[f.busFirst+1])+6) > 0.05 {
					t.Fatalf("frame n=%d, bus 2 %v dBFS", fr.Count, fr.Levels[f.busFirst+1])
				}
			})
		})
	}
}
