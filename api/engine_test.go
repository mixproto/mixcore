// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	consoleIP = "10.0.0.1"
	configOK  = `{"version":0,"family":"x32","host":"10.0.0.1","meters":{"inputs":true}}`
)

type recorder struct {
	mu   sync.Mutex
	msgs []stateJSON
	raw  []string
}

func (r *recorder) OnState(s string) {
	var m stateJSON
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.raw = append(r.raw, s)
	r.mu.Unlock()
}

func (r *recorder) last() stateJSON {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return stateJSON{}
	}
	return r.msgs[len(r.msgs)-1]
}

func (r *recorder) all() []stateJSON {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stateJSON(nil), r.msgs...)
}

type env struct {
	sim *simx32.Sim
	eng *Engine
	rec *recorder
}

// newEnv runs a simulator and an engine on an in-memory network (call inside synctest).
func newEnv(t *testing.T) *env {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":10023")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{Name: "FOH"})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2")})
	rec := &recorder{}
	eng.SetStateListener(rec)
	t.Cleanup(func() {
		_ = eng.Stop()
		cancel()
		wg.Wait()
		_ = pc.Close()
	})
	return &env{sim: sim, eng: eng, rec: rec}
}

func (e *env) startLive(t *testing.T) {
	t.Helper()
	if _, err := e.eng.Start(configOK); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(time.Second)
	synctest.Wait()
	if st := e.rec.last(); st.Status != "live" {
		t.Fatalf("status = %q, want live", st.Status)
	}
}

func cmd(field, value, origin string) string {
	v := ""
	if value != "" {
		v = `,"value":` + value
	}
	return `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"` + field + `"}` + v +
		`,"origin":{"kind":"` + origin + `","clientId":"ui","seq":9}}`
}

func TestStart_RejectsBadConfig(t *testing.T) {
	eng := NewEngine()
	bad := []string{
		``, `{}`, `{"version":1,"family":"x32","host":"10.0.0.1"}`,
		`{"version":0,"family":"wing","host":"10.0.0.1"}`,
		`{"version":0,"family":"x32","host":"console.local"}`,
		`{"version":0,"family":"x32","host":"10.0.0.1","port":70000}`,
		`{"version":0,"family":"x32","host":"10.0.0.1","extra":1}`,
		`{"version":0,"family":"x32","host":"10.0.0.1"} {}`,
	}
	for _, c := range bad {
		if _, err := eng.Start(c); CodeOf(err) != CodeInvalidConfig || err.Error() != CodeInvalidConfig {
			t.Fatalf("Start(%q) = %v, want %s", c, err, CodeInvalidConfig)
		}
	}
	if err := eng.Apply(cmd("fader", "0", "gesture")); CodeOf(err) != CodeNotStarted {
		t.Fatalf("Apply before Start = %v", err)
	}
	if err := eng.Stop(); CodeOf(err) != CodeNotStarted {
		t.Fatalf("Stop before Start = %v", err)
	}
	if CodeOf(nil) != "" || CodeOf(errors.New("x")) != CodeInternal {
		t.Fatal("CodeOf mismatch")
	}
}

func TestEngine_ConnectsAndSendsSnapshotThenDiffs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		desc, err := e.eng.Start(configOK)
		if err != nil {
			t.Fatal(err)
		}
		if desc != `{"version":0,"family":"x32","endpoint":{"host":"10.0.0.1","port":10023},"faderLaw":"x32"}` {
			t.Fatalf("session = %s", desc)
		}
		if _, err := e.eng.Start(configOK); CodeOf(err) != CodeAlreadyStarted {
			t.Fatalf("second Start = %v", err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		snap := e.rec.last()
		if snap.Type != "snapshot" || snap.Status != "live" || len(snap.Strips) != 33 || snap.Console.Name != "FOH" {
			t.Fatalf("snapshot = %+v", snap)
		}
		if snap.Strips[0].FaderDB != minusInf || snap.Strips[32].Kind != "main" {
			t.Fatalf("strips: first %+v, last %+v", snap.Strips[0], snap.Strips[32])
		}
		_ = e.sim.SurfaceSet("/ch/04/mix/on", osc.Int(0))
		synctest.Wait()
		diff := e.rec.last()
		if diff.Type != "diff" || len(*diff.Changes) != 1 || (*diff.Changes)[0].Origin.Kind != "console" || (*diff.Changes)[0].Value != true {
			t.Fatalf("diff = %+v", diff)
		}
		for i, m := range e.rec.all() {
			if m.Seq != uint64(i+1) {
				t.Fatalf("message %d has seq %d; seq must grow by one", i, m.Seq)
			}
		}
		if len(e.sim.Writes()) != 0 {
			t.Fatal("connecting must not write")
		}
	})
}

func TestApply_WritesAndEchoCarriesGestureOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		if err := e.eng.Apply(cmd("fader", `-10`, "gesture")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if v, _ := e.sim.Value("/ch/01/mix/fader"); math.Abs(float64(v.Float)-0.5) > 1e-3 {
			t.Fatalf("console fader = %v, want 0.5", v.Float)
		}
		d := e.rec.last()
		if d.Type != "diff" || (*d.Changes)[0].Origin != (originJSON{Kind: "gesture", ClientID: "ui", Seq: 9}) {
			t.Fatalf("echo diff = %+v", d)
		}
		if err := e.eng.Apply(cmd("fader", `"-inf"`, "macro")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if v, _ := e.sim.Value("/ch/01/mix/fader"); v.Float != 0 {
			t.Fatalf("-inf must send 0.0, got %v", v.Float)
		}
		if n := len(e.sim.Writes()); n != 2 {
			t.Fatalf("%d writes, want 2", n)
		}
	})
}

func TestApply_ErrorCodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		tests := []struct {
			name, in, want string
		}{
			{"not JSON", `{`, CodeInvalidCommand},
			{"wrong version", strings.Replace(cmd("mute", "true", "gesture"), `"version":0`, `"version":2`, 1), CodeInvalidCommand},
			{"unknown field", cmd("pan", "0", "gesture"), CodeInvalidCommand},
			{"value of the wrong type", cmd("mute", `"yes"`, "gesture"), CodeInvalidCommand},
			{"level string other than -inf", cmd("fader", `"+inf"`, "gesture"), CodeInvalidCommand},
			{"set without value", cmd("mute", "", "gesture"), CodeInvalidCommand},
			{"unknown origin", cmd("mute", "true", "timer"), CodeInvalidCommand},
			{"console origin", cmd("mute", "true", "console"), CodeNotUserOrigin},
			{"system origin", cmd("mute", "true", "system"), CodeNotUserOrigin},
			{"name too long", cmd("name", `"a much too long name"`, "gesture"), CodeInvalidValue},
			{"channel 40", strings.Replace(cmd("mute", "true", "gesture"), `"index":1`, `"index":40`, 1), CodeUnsupported},
			{"main with index", strings.Replace(cmd("mute", "true", "gesture"), `"kind":"input","index":1`, `"kind":"main","index":2`, 1), CodeInvalidCommand},
			{"hold on mute", `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":1},"field":"mute"},"origin":{"kind":"gesture"}}`, CodeUnsupported},
			{"hold with value", `{"version":0,"type":"holdEnd","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":1,"origin":{"kind":"gesture"}}`, CodeInvalidCommand},
		}
		for _, tc := range tests {
			if err := e.eng.Apply(tc.in); CodeOf(err) != tc.want {
				t.Fatalf("%s: Apply = %v, want %s", tc.name, err, tc.want)
			}
		}
		synctest.Wait()
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("rejected commands produced %d writes", n)
		}
	})
}

func TestStale_ApplyReturnsErrStaleAndReconnectReReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		e.sim.Pause()
		time.Sleep(x32.DefaultTiming.DeadAfter + x32.DefaultTiming.PingEvery)
		synctest.Wait()
		if st := e.rec.last(); st.Status != "stale" {
			t.Fatalf("status = %q, want stale", st.Status)
		}
		if err := e.eng.Apply(cmd("mute", "true", "gesture")); CodeOf(err) != CodeStale {
			t.Fatalf("Apply while stale = %v", err)
		}
		_ = e.sim.SurfaceSet("/ch/01/config/name", osc.String("Changed"))
		e.sim.Resume()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		st := e.rec.last()
		if st.Status != "live" || st.Type != "snapshot" || st.Strips[0].Name != "Changed" {
			t.Fatalf("after reconnect: %+v", st)
		}
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("%d writes during outage and reconnect, want 0", n)
		}
	})
}

func TestResume_RereadsAndWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		if err := e.eng.Pause(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if st := e.rec.last(); st.Status != "stopped" {
			t.Fatalf("after Pause status = %q", st.Status)
		}
		time.Sleep(15 * time.Second) // the /xremote registration lapses (X32-F20)
		if len(e.sim.Subscribers()) != 0 {
			t.Fatal("paused engine still subscribed")
		}
		_ = e.sim.SurfaceSet("/ch/02/mix/fader", osc.Float(0.75))
		if err := e.eng.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		st := e.rec.last()
		ch2, _ := st.Strips[1].FaderDB.(float64)
		if st.Status != "live" || st.Type != "snapshot" || math.Abs(ch2) > 0.05 { // 0.75 is step 767 (X32-F41)
			t.Fatalf("after Resume: status %q type %q ch2 %v", st.Status, st.Type, st.Strips[1].FaderDB)
		}
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("Pause/Resume wrote %d times", n)
		}
	})
}

func TestHold_ExternalChangeEndsGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		hold := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":1},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`
		if err := e.eng.Apply(hold); err != nil {
			t.Fatal(err)
		}
		if err := e.eng.Apply(cmd("fader", "-3", "gesture")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if d := e.rec.last(); len(d.Events) != 0 {
			t.Fatalf("our own echo ended the gesture: %+v", d.Events)
		}
		_ = e.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.5)) // -10 dB at the desk
		synctest.Wait()
		d := e.rec.last()
		if d.Type != "diff" || len(*d.Changes) != 1 || len(d.Events) != 1 {
			t.Fatalf("diff = %+v", d)
		}
		ev := d.Events[0]
		if ev.Type != "externalChangeOnHeld" || ev.ClientID != "ui" || ev.Origin.Kind != "console" || math.Abs(ev.Value.(float64)+10) > 0.05 {
			t.Fatalf("event = %+v", ev)
		}
		writes := len(e.sim.Writes())
		if err := e.eng.Apply(cmd("fader", "-4", "gesture")); CodeOf(err) != CodeGestureEnded {
			t.Fatalf("Apply after the external change = %v, want %s", err, CodeGestureEnded)
		}
		synctest.Wait()
		if len(e.sim.Writes()) != writes {
			t.Fatal("an ended gesture reached the console")
		}
		if err := e.eng.Apply(hold); err != nil {
			t.Fatal(err)
		}
		if err := e.eng.Apply(cmd("fader", "-4", "gesture")); err != nil {
			t.Fatalf("new hold: %v", err)
		}
	})
}

func TestMeterFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		if b := e.eng.MeterFrame(); len(b) != meters.FrameHeaderSize || binary.LittleEndian.Uint16(b[6:]) != 0 {
			t.Fatalf("frame before start = %x", b)
		}
		e.sim.SetInputLevels([]float32{1})
		e.startLive(t)
		b := e.eng.MeterFrame()
		if string(b[:4]) != meters.FrameMagic || binary.LittleEndian.Uint16(b[6:]) != 32 {
			t.Fatalf("live frame header = %x", b[:meters.FrameHeaderSize])
		}
		if l := math.Float32frombits(binary.LittleEndian.Uint32(b[24:])); l != 0 {
			t.Fatalf("input 1 = %v dBFS, want 0", l)
		}
		_ = e.eng.Pause()
		if b := e.eng.MeterFrame(); binary.LittleEndian.Uint16(b[6:]) != 0 {
			t.Fatal("meters must be empty while paused")
		}
	})
}

func TestFaderLawCalls(t *testing.T) {
	eng := NewEngine()
	if got := eng.FaderPosition(0); got != 0.75 {
		t.Fatalf("FaderPosition(0) = %v", got)
	}
	if got := eng.FaderPosition(math.Inf(-1)); got != 0 {
		t.Fatalf("FaderPosition(-inf) = %v", got)
	}
	if got := eng.FaderDB(0); !math.IsInf(got, -1) {
		t.Fatalf("FaderDB(0) = %v", got)
	}
	if got := eng.FaderDB(0.5); got != -10 {
		t.Fatalf("FaderDB(0.5) = %v", got)
	}
	empty := NewEngineWith(Options{Drivers: map[string]driver.Driver{}})
	if empty.FaderPosition(0) != 0 || !math.IsInf(empty.FaderDB(1), -1) {
		t.Fatal("an engine without a law must return safe values")
	}
}

func TestStopThenStartAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		if err := e.eng.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := e.eng.Resume(); CodeOf(err) != CodeNotStarted {
			t.Fatalf("Resume after Stop = %v", err)
		}
		if err := e.eng.RequestSnapshot(); CodeOf(err) != CodeNotStarted {
			t.Fatalf("RequestSnapshot after Stop = %v", err)
		}
		e.startLive(t)
		if err := e.eng.RequestSnapshot(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if e.rec.last().Type != "snapshot" {
			t.Fatal("RequestSnapshot must deliver a snapshot")
		}
		e.eng.SetStateListener(nil)
	})
}
