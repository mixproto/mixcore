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
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const (
	consoleIP = "10.0.0.1"
	clientIP  = "10.0.0.2"
)

var gesture = driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 1}

// rig is a simulator and a driver on one in-memory network.
type rig struct {
	t     *testing.T
	net   *memnet.Network
	sim   *simx32.Sim
	drv   *Driver
	slot  *meters.Slot
	mu    sync.Mutex
	audit []driver.WriteRecord
}

func newRig(t *testing.T, simOpts simx32.Options) *rig {
	t.Helper()
	n := memnet.New()
	r := &rig{t: t, net: n, sim: simx32.New(simOpts), drv: New(Config{}), slot: &meters.Slot{}}
	r.serveSim(consoleIP)
	return r
}

func (r *rig) serveSim(ip string) {
	pc, err := r.net.ListenPacket(ip + ":10023")
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = r.sim.Serve(ctx, pc) })
	r.t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
}

func (r *rig) opts() driver.Options {
	return driver.Options{
		ListenPacket: r.net.Host(clientIP),
		Meters:       r.slot,
		WriteAudit: func(w driver.WriteRecord) {
			r.mu.Lock()
			r.audit = append(r.audit, w)
			r.mu.Unlock()
		},
	}
}

func (r *rig) connect() driver.Session {
	r.t.Helper()
	sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, r.opts())
	if err != nil {
		r.t.Fatalf("Connect: %v", err)
	}
	r.t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// run starts Run and returns the event channel and a function that stops it
// and returns Run's error.
func (r *rig) run(sess driver.Session) (<-chan driver.Event, func() error) {
	events := make(chan driver.Event, 256)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx, events) }()
	var once sync.Once
	var err error
	stop := func() error { once.Do(func() { cancel(); err = <-done }); return err }
	r.t.Cleanup(func() { _ = stop() })
	return events, stop
}

// assertWritesAudited joins the simulator's write log with the driver's audit:
// every write the console received matches exactly one audited user write.
func (r *rig) assertWritesAudited() {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	writes := r.sim.Writes()
	if len(writes) != len(r.audit) {
		r.t.Fatalf("console received %d writes, driver audited %d", len(writes), len(r.audit))
	}
	for i, w := range writes {
		a := r.audit[i]
		if w.Address != a.Address || !a.Origin.MayWrite() {
			r.t.Fatalf("write %d: console got %s, audit has %s with origin %v", i, w.Address, a.Address, a.Origin)
		}
	}
}

func ref(i int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: i}, Field: f}
}

func level(t *testing.T, db float64) model.Value { return mustLevel(t, db) }

func TestConnect_ReadsFullStateAndSendsNoWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{Name: "FOH", Model: "M32"})
		_ = r.sim.SurfaceSet("/ch/07/config/name", osc.String("Vox"))
		_ = r.sim.SurfaceSet("/ch/07/mix/fader", osc.Float(0.75))
		_ = r.sim.SurfaceSet("/ch/07/mix/on", osc.Int(0))
		_ = r.sim.SurfaceSet("/main/st/mix/fader", osc.Float(0.5))

		sess := r.connect()

		if id := sess.Identity(); id.Name != "FOH" || id.Model != "M32" || id.Address != consoleIP {
			t.Fatalf("identity = %+v", id)
		}
		st := sess.Snapshot()
		ch7, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 7})
		if ch7.Name != "Vox" || !ch7.Muted || math.Abs(ch7.FaderDB) > 0.05 {
			t.Fatalf("ch7 = %+v", ch7)
		}
		if main, _ := st.Main(); math.Abs(main.FaderDB+10) > 0.05 { // 0.5 is stored as step 512 (X32-F41)
			t.Fatalf("main = %+v", main)
		}
		if len(st.Inputs()) != inputChannels {
			t.Fatalf("%d inputs", len(st.Inputs()))
		}
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("Connect sent %d writes, want 0: %+v", len(w), w) // safety rule 1
		}
		if subs := r.sim.Subscribers(); len(subs) != 1 {
			t.Fatalf("Connect must register /xremote, subscribers = %v", subs)
		}
	})
}

func TestConnect_FailsWhenNothingAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.sim.Pause()
		_, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, r.opts())
		if !errors.Is(err, errNoReply) {
			t.Fatalf("err = %v, want errNoReply", err)
		}
		if _, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: "console.local"}, r.opts()); !errors.Is(err, ErrBadEndpoint) {
			t.Fatalf("hostname endpoint err = %v", err)
		}
	})
}

func TestApply_RejectsNonUserOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		r.run(sess)
		for _, k := range []driver.OriginKind{driver.OriginConsole, driver.OriginSystem, 0} {
			err := sess.Apply(driver.Command{Param: ref(1, model.FieldFader), Value: level(t, 0), Origin: driver.Origin{Kind: k}})
			if !errors.Is(err, driver.ErrNotUserOrigin) {
				t.Fatalf("origin %v: err = %v", k, err)
			}
		}
		synctest.Wait()
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("non-user origin reached the console: %+v", w) // safety rule 2
		}
	})
}

func TestApply_ValidatesBeforeQueueing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		bus := model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 40}, Field: model.FieldFader}
		if err := sess.Apply(driver.Command{Param: bus, Value: level(t, 0), Origin: gesture}); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("unknown param err = %v", err)
		}
		long, _ := model.Text("much too long name")
		if err := sess.Apply(driver.Command{Param: ref(1, model.FieldName), Value: long, Origin: gesture}); !errors.Is(err, model.ErrInvalidValue) {
			t.Fatalf("long name err = %v", err)
		}
	})
}

func TestEcho_OwnWriteDoesNotBounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)

		if err := sess.Apply(driver.Command{Param: ref(3, model.FieldFader), Value: level(t, -10), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		ev := (<-events).(driver.ParamChanged)
		if ev.Param != ref(3, model.FieldFader) || ev.Origin != gesture {
			t.Fatalf("echo event = %+v, want our write with its gesture origin", ev)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := len(r.sim.Writes()); n != 1 {
			t.Fatalf("console received %d writes, want exactly 1 (no bounce)", n)
		}
		r.assertWritesAudited()
	})
}

func TestSurfaceChange_ReportedWithConsoleOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		_ = r.sim.SurfaceSet("/ch/12/mix/on", osc.Int(0))
		ev := (<-events).(driver.ParamChanged)
		if ev.Param != ref(12, model.FieldMute) || ev.Origin.Kind != driver.OriginConsole || !ev.Value.Equal(model.Bool(true)) {
			t.Fatalf("event = %+v", ev)
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("a console change must not cause a write")
		}
	})
}

func TestApply_CoalescesToLatestValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		for _, db := range []float64{-30, -20, -10, -5} {
			if err := sess.Apply(driver.Command{Param: ref(1, model.FieldFader), Value: level(t, db), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
		}
		r.run(sess)
		synctest.Wait()
		w := r.sim.Writes()
		want, _ := dbToFader(-5)
		if len(w) != 1 || w[0].Arg.Float != want {
			t.Fatalf("writes = %+v, want one write of the latest value %v", w, want)
		}
	})
}

func TestClose_DropsQueuedWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		if err := sess.Apply(driver.Command{Param: ref(1, model.FieldMute), Value: model.Bool(true), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		_ = sess.Close()
		_ = sess.Close() // idempotent
		synctest.Wait()
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("a queued write was sent after Close: %+v", w) // safety rule 3
		}
		if err := sess.Apply(driver.Command{Param: ref(1, model.FieldMute), Value: model.Bool(true), Origin: gesture}); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("Apply after Close err = %v", err)
		}
		if err := sess.Run(context.Background(), make(chan driver.Event)); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("Run after Close err = %v", err)
		}
	})
}

func TestIdleSession_NoWritesForAnHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		_, stop := r.run(sess)
		time.Sleep(time.Hour)
		synctest.Wait()
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("idle session wrote %d times", len(w)) // safety rule 2: timers never write
		}
		if len(r.sim.Subscribers()) != 1 {
			t.Fatal("renewals must keep /xremote alive for the whole hour (X32-F20, X32-F27)")
		}
		if err := stop(); err != nil {
			t.Fatalf("Run returned %v after a healthy hour", err)
		}
	})
}

func TestRun_DetectsLostConsole(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		_, stop := r.run(sess)
		start := time.Now()
		r.sim.Pause()
		time.Sleep(DefaultTiming.DeadAfter + DefaultTiming.PingEvery)
		if err := stop(); !errors.Is(err, driver.ErrConnectionLost) {
			t.Fatalf("Run err = %v, want ErrConnectionLost", err)
		}
		if took := time.Since(start); took > DefaultTiming.DeadAfter+DefaultTiming.PingEvery {
			t.Fatalf("loss detected after %s", took)
		}
		if err := sess.Apply(driver.Command{Param: ref(1, model.FieldMute), Value: model.Bool(true), Origin: gesture}); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("Apply on a dead session err = %v", err)
		}
	})
}

// Meters and pushes keep arriving after our uplink dies; only missing /xinfo
// replies reveal it (X32-F25).
func TestRun_DetectsUplinkOnlyLossDespiteMeters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		_, stop := r.run(sess)
		_ = sess.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(time.Second)
		r.net.SetDropFrom(clientIP, true)
		time.Sleep(DefaultTiming.DeadAfter + DefaultTiming.PingEvery)
		if err := stop(); !errors.Is(err, driver.ErrConnectionLost) {
			t.Fatalf("Run err = %v, want ErrConnectionLost while meters still flow", err)
		}
	})
}

func TestMeters_InputLevelsPublishedInDBFS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.sim.SetInputLevels([]float32{1, 0.5, 0})
		sess := r.connect()
		r.run(sess)
		if err := sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		var fr meters.Frame
		if !r.slot.CopyLatest(&fr) || fr.Count != inputChannels {
			t.Fatalf("frame count = %d", fr.Count)
		}
		if fr.Levels[0] != 0 || math.Abs(float64(fr.Levels[1])+6.0206) > 1e-3 || !math.IsInf(float64(fr.Levels[2]), -1) {
			t.Fatalf("levels = %v", fr.Levels[:3])
		}
		time.Sleep(20 * time.Second) // renewals keep the 10 s stream alive (X32-F50)
		before := fr.Seq
		time.Sleep(time.Second)
		if !r.slot.CopyLatest(&fr) || fr.Seq == before {
			t.Fatal("meters stopped although SetMeters is on")
		}
		_ = sess.SetMeters(driver.MeterRequest{})
		synctest.Wait()
		r.slot.CopyLatest(&fr) // a frame already in flight may land before the switch takes effect
		time.Sleep(15 * time.Second)
		before = fr.Seq
		time.Sleep(time.Second)
		if r.slot.CopyLatest(&fr) && fr.Seq != before {
			t.Fatal("meters still published after SetMeters off")
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("meter requests must not be writes")
		}
	})
}

func TestProbe_FindsBroadcastAndManualConsoles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{Name: "A"})
		other := simx32.New(simx32.Options{Name: "B"})
		pc, _ := r.net.ListenPacket("10.0.9.9:10023")
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { _ = other.Serve(ctx, pc) })
		defer func() { cancel(); wg.Wait(); _ = pc.Close() }()

		found, err := Probe(context.Background(), r.net.Host(clientIP),
			[]netip.AddrPort{LimitedBroadcast, netip.MustParseAddrPort("10.0.9.9:10023")}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, f := range found {
			names[f.Identity.Name] = true
		}
		if len(found) != 2 || !names["A"] || !names["B"] {
			t.Fatalf("found %+v, want A and B once each", found)
		}
		if len(r.sim.Writes())+len(other.Writes()) != 0 {
			t.Fatal("a probe must not write")
		}
	})
}

func TestDirectedBroadcast(t *testing.T) {
	ip, ipn, _ := net.ParseCIDR("192.168.1.62/24")
	ipn.IP = ip
	bc, ok := directedBroadcast(ipn)
	if !ok || bc.String() != "192.168.1.255:10023" {
		t.Fatalf("directedBroadcast = %v, %v", bc, ok)
	}
	if len(BroadcastTargets()) < 1 {
		t.Fatal("BroadcastTargets must include the limited broadcast")
	}
}
