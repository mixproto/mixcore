// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

const configXAir = `{"version":0,"family":"xair","host":"10.0.0.1","meters":{"inputs":true}}`

type xairEnv struct {
	net *memnet.Network
	sim *simxair.Sim
	eng *Engine
	rec *recorder
}

// newXAirEnv runs an X Air simulator and an engine on an in-memory network (inside synctest).
func newXAirEnv(t *testing.T, simOpts simxair.Options) *xairEnv {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":10024")
	if err != nil {
		t.Fatal(err)
	}
	sim := simxair.New(simOpts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2")})
	rec := &recorder{}
	eng.SetStateListener(rec)
	t.Cleanup(func() {
		eng.Close()
		cancel()
		wg.Wait()
		_ = pc.Close()
	})
	return &xairEnv{net: n, sim: sim, eng: eng, rec: rec}
}

func (e *xairEnv) startLive(t *testing.T) string {
	t.Helper()
	desc, err := e.eng.Start(configXAir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(time.Second)
	synctest.Wait()
	if st := e.rec.last(); st.Status != "live" {
		t.Fatalf("status = %q, want live", st.Status)
	}
	return desc
}

// The engine starts an X Air session: session description, a snapshot with
// 16 inputs, 4 mute groups, 4 DCAs and a main without mute-group membership,
// and nothing written.
func TestXAir_StartSnapshotAndSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{Name: "XR18-1D-DA-B4", Model: "XR18"})
		_ = e.sim.SurfaceSet("/ch/01/grp/mute", osc.Int(1))
		desc := e.startLive(t)
		if desc != `{"version":0,"family":"xair","endpoint":{"host":"10.0.0.1","port":10024},"faderLaw":"xair"}` {
			t.Fatalf("session = %s", desc)
		}
		if err := e.eng.RequestSnapshot(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		e.rec.mu.Lock()
		raw := e.rec.raw[len(e.rec.raw)-1]
		e.rec.mu.Unlock()
		st := e.rec.last()
		if st.Type != "snapshot" || len(st.Strips) != 17 || len(st.MuteGroups) != 4 || len(st.DCAs) != 4 {
			t.Fatalf("snapshot: %d strips, %d mute groups, %d dcas", len(st.Strips), len(st.MuteGroups), len(st.DCAs))
		}
		main := st.Strips[16]
		if main.Kind != "main" || main.MuteGroups != nil || main.EffectiveMute == nil || st.Strips[0].MuteGroups == nil {
			t.Fatalf("main %+v; input 1 %+v", main, st.Strips[0])
		}
		if !strings.Contains(raw, `{"kind":"main","name":"","faderDB":"-inf","mute":false,"effectiveMute":`) {
			t.Fatalf("main must have no muteGroups field: %s", raw)
		}
		if st.Console == nil || st.Console.Model != "XR18" {
			t.Fatalf("console %+v", st.Console)
		}
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("start wrote %d times", n)
		}
	})
}

// A gesture reaches the mixer and comes back with its origin; meters are 18
// levels (16 inputs, Main L/R); FaderPosition uses the X Air law.
func TestXAir_ApplyEchoAndMeters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		e.sim.SetMeterValues([]int16{-4608})
		e.sim.SetMeterValue(simxair.Bank1MainL, -256)
		e.startLive(t)
		if err := e.eng.Apply(cmd("fader", "-10", "gesture")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if v, _ := e.sim.Value("/ch/01/mix/fader"); v.Float != 512.0/1023 {
			t.Fatalf("mixer fader %v", v.Float)
		}
		var echoed bool
		for _, m := range e.rec.all() {
			if m.Changes != nil {
				for _, c := range *m.Changes {
					echoed = echoed || (c.Param.Field == "fader" && c.Origin.Kind == "gesture")
				}
			}
		}
		if !echoed {
			t.Fatal("no echo of the gesture in a diff")
		}
		buf := make([]byte, MeterFrameMaxBytes)
		n := e.eng.ReadMeterFrame(buf)
		if count := binary.LittleEndian.Uint16(buf[6:]); n != 24+4*18 || count != 18 {
			t.Fatalf("meter frame %d bytes, n = %d", n, count)
		}
		l0 := math.Float32frombits(binary.LittleEndian.Uint32(buf[24:]))
		mainL := math.Float32frombits(binary.LittleEndian.Uint32(buf[24+4*16:]))
		if l0 != -18 || mainL != -1 {
			t.Fatalf("input 1 %v dBFS, Main L %v dBFS", l0, mainL)
		}
		if e.eng.FaderPosition(0) != 0.75 || e.eng.FaderStep(9.9, 1) != 10 {
			t.Fatal("fader law")
		}
	})
}

// A snapshot load at the mixer with /-snap/index pushed reaches the app as
// sceneRecalledAtConsole with loadedScene and the snapshotIndex signal.
func TestXAir_SceneRecallEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{PushSnapIndex: true})
		scene := map[string]osc.Arg{}
		for ch := 1; ch <= 10; ch++ {
			scene[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.5)
		}
		if err := e.sim.StoreSnapshot(7, "Worship", scene); err != nil {
			t.Fatal(err)
		}
		e.startLive(t)
		if err := e.sim.LoadSnapshot(7); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		e.rec.mu.Lock()
		joined := strings.Join(e.rec.raw, "\n")
		e.rec.mu.Unlock()
		want := `{"type":"sceneRecalledAtConsole","confidence":"high","signals":["burst","snapshotIndex"],"loadedScene":7}`
		if !strings.Contains(joined, want) {
			t.Fatalf("no %s in\n%s", want, joined)
		}
	})
}

// Discovery: family xair probes 10024; replies are classified by port and
// model, so the X32 on the same LAN is listed only for family x32.
func TestXAir_DiscoverClassifiesFamilies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{Name: "MR18-AA", Model: "MR18V2"})
		x := simx32.New(simx32.Options{Name: "FOH"})
		pc, _ := e.net.ListenPacket("10.0.0.9:10023")
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { _ = x.Serve(ctx, pc) })
		defer func() { cancel(); wg.Wait(); _ = pc.Close() }()
		for family, wantName := range map[string]string{"xair": "MR18-AA", "x32": "FOH"} {
			lis := &discoveryRecorder{}
			e.eng.SetDiscoveryListener(lis)
			req := fmt.Sprintf(`{"version":0,"family":%q,"broadcast":true,"hosts":[{"host":"10.0.0.9"},{"host":"10.0.0.9","port":10023},{"host":"10.0.0.1","port":10024}],"waitMs":500}`, family)
			if err := e.eng.Discover(req); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Second)
			synctest.Wait()
			got := lis.all()
			if len(got) != 1 {
				t.Fatalf("%s: %d results", family, len(got))
			}
			res := got[0]
			if len(res.Consoles) != 1 || res.Consoles[0].Name != wantName || res.Consoles[0].Family != family {
				t.Fatalf("%s: %+v", family, res.Consoles)
			}
		}
	})
}

func TestClassify_PortAndModel(t *testing.T) {
	for _, tc := range []struct {
		port  int
		model string
		want  string
	}{
		{10023, "X32", familyX32}, {10023, "M32R", familyX32}, {10023, "X32 FUTURE", familyX32}, {10023, "XR18", ""},
		{10024, "XR18", familyXAir}, {10024, "MR12V2", familyXAir}, {10024, "XR18NEW", familyXAir}, {10024, "X32", ""},
		{9000, "X18", familyXAir}, {9000, "X32RACK", familyX32}, {9000, "WING", ""},
	} {
		f := x32.Found{Endpoint: driver.Endpoint{Port: tc.port}, Identity: driver.Identity{Model: tc.model}}
		if got := classify(f); got != tc.want {
			t.Errorf("port %d model %q: %q, want %q", tc.port, tc.model, got, tc.want)
		}
	}
}
