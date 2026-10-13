// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// TestSimSessionBuses_Log shows core-api v0.4 against sim/x32 and sim/xair on
// real loopback UDP with the default timing: the bus list and an input's
// sends with the console's tap words, an engineer's send gesture and its
// echo, a desk change that ends it, a monitor seat (second engine, role
// monitor of bus 2) refused outside its mix and stopped at its ceiling, the
// read-only tap, and the X Air sends without on/off. It writes a
// timestamped log when MIXCORE_BUSES_SESSION_LOG names the file:
//
//	MIXCORE_BUSES_SESSION_LOG=/tmp/buses.log go test -tags=sim -run TestSimSessionBuses_Log -v ./api/
func TestSimSessionBuses_Log(t *testing.T) {
	path := os.Getenv("MIXCORE_BUSES_SESSION_LOG")
	if path == "" {
		t.Skip("set MIXCORE_BUSES_SESSION_LOG to write the session log")
	}
	f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := &busSession{out: io.MultiWriter(f, testWriter{t}), start: time.Now()}
	s.x32(t)
	s.xair(t)
}

type busSession struct {
	out   io.Writer
	start time.Time
	mu    sync.Mutex
	audit []driver.WriteRecord
}

func (s *busSession) logf(format string, args ...any) {
	fmt.Fprintf(s.out, "[%8.3f s] %s\n", time.Since(s.start).Seconds(), fmt.Sprintf(format, args...))
}

// lastState collects the newest state message of one engine.
type lastState struct {
	mu  sync.Mutex
	raw []string
}

func (l *lastState) OnState(js string) { l.mu.Lock(); l.raw = append(l.raw, js); l.mu.Unlock() }

func (l *lastState) waitFor(t *testing.T, what string, match func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		raw := append([]string(nil), l.raw...)
		l.mu.Unlock()
		for i := len(raw) - 1; i >= 0; i-- {
			if match(raw[i]) {
				return raw[i]
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return ""
}

func (s *busSession) engine(t *testing.T, cfg string) (*Engine, *lastState) {
	t.Helper()
	eng := NewEngineWith(Options{WriteAudit: func(w driver.WriteRecord) { s.mu.Lock(); s.audit = append(s.audit, w); s.mu.Unlock() }})
	l := &lastState{}
	eng.SetStateListener(l)
	t.Cleanup(eng.Close)
	t0 := time.Now()
	if _, err := eng.Start(cfg); err != nil {
		t.Fatal(err)
	}
	l.waitFor(t, "live", func(m string) bool {
		return strings.Contains(m, `"status":"live"`) && strings.Contains(m, `"type":"snapshot"`)
	})
	s.logf("live in %d ms with %s", time.Since(t0).Milliseconds(), cfg)
	return eng, l
}

func serveBusSim(t *testing.T, serve func(context.Context, net.PacketConn) error) int {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func (s *busSession) x32(t *testing.T) {
	sim := simx32.New(simx32.Options{Name: "X32 FOH (sim)"})
	for addr, a := range map[string]osc.Arg{ // acceptance fixture (dybyskz-mix docs/design/acceptance/README.md), reduced
		"/bus/01/config/name": osc.String("Mon 1 Drums"), "/bus/02/config/name": osc.String("Mon 2 Keys"),
		"/bus/03/config/name": osc.String("Mon 3 Bass"), "/bus/02/config/color": osc.Int(4), "/config/buslink/1-2": osc.Int(0),
		"/ch/04/config/name": osc.String("Keys"), "/ch/04/mix/02/level": osc.Float(128.0 / 160), "/ch/04/mix/01/type": osc.Int(3),
		"/ch/05/mix/02/on": osc.Int(0), "/bus/02/mix/fader": osc.Float(0.675),
	} {
		if err := sim.SurfaceSet(addr, a); err != nil {
			t.Fatal(err)
		}
	}
	port := serveBusSim(t, sim.Serve)
	s.logf("sim/x32 on 127.0.0.1:%d (fixture: bus 2 Mon 2 Keys blue, ch 4 Keys sends +2.0 dB PRE to bus 2, ch 5 send off)", port)
	cfg := fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d}`, port)
	foh, fohState := s.engine(t, cfg)
	snap := fohState.waitFor(t, "snapshot", func(m string) bool { return strings.Contains(m, `"buses"`) })
	s.logSnapshot(snap)
	s.engineerGesture(t, sim, foh, fohState)
	mon, _ := s.engine(t, strings.TrimSuffix(cfg, "}")+`,"role":{"kind":"monitor","buses":[{"bus":2}]}}`)
	s.monitorSeat(t, sim, mon)
	s.auditJoin(t, sim.Writes())
}

func (s *busSession) logSnapshot(snap string) {
	var m stateJSON
	_ = json.Unmarshal([]byte(snap), &m)
	s.logf("snapshot: %d buses, %d matrices, sendFields %+v", len(m.Buses), len(m.Matrices), *m.SendFields)
	for _, b := range m.Buses[:3] {
		s.logf("  bus %d %q kind %s colour %s fader %v mute %v linked %v", b.Index, b.Name, b.Kind, b.Colour, b.FaderDB, b.Mute, deref(b.Linked))
	}
	for _, sd := range m.Strips[3].Sends[:3] {
		s.logf("  ch 4 %q send to bus %d: level %v on %v pan %v tap %q", m.Strips[3].Name, sd.Bus, sd.LevelDB, deref(sd.On), derefF(sd.Pan), sd.Tap)
	}
	labels := make([]string, 0, len(m.SendTaps))
	for _, l := range m.SendTaps {
		labels = append(labels, l.Tap+"="+l.Label)
	}
	s.logf("  tap words (CR-16): %s", strings.Join(labels, " "))
}

func deref(b *bool) any {
	if b == nil {
		return "absent"
	}
	return *b
}

func derefF(f *float64) any {
	if f == nil {
		return "absent"
	}
	return *f
}

func (s *busSession) engineerGesture(t *testing.T, sim *simx32.Sim, foh *Engine, st *lastState) {
	t.Helper()
	res, err := foh.Gesture(sendCmd("holdBegin", "sendLevel", 4, 2, "", "foh"))
	s.logf("engineer holdBegin ch 4 send bus 2: %s %v", res, err)
	res, err = foh.Gesture(sendCmd("set", "sendLevel", 4, 2, "4", "foh"))
	s.logf("engineer set +4.0 dB: %s %v", res, err)
	echo := st.waitFor(t, "the echo", func(m string) bool {
		return strings.Contains(m, `"bus":2},"value":4,"origin":{"kind":"gesture","clientId":"foh"`)
	})
	s.logf("echo diff carries the gesture origin: %s", excerpt(echo, `"changes"`, 160))
	w := sim.Writes()
	s.logf("console write log: %d write(s), last %s %v", len(w), w[len(w)-1].Address, w[len(w)-1].Arg.Float)
	if err := sim.SurfaceSet("/ch/04/mix/02/level", osc.Float(0.5)); err != nil {
		t.Fatal(err)
	}
	ended := st.waitFor(t, "external change", func(m string) bool { return strings.Contains(m, "externalChangeOnHeld") })
	s.logf("desk moved it to -10.0 dB: %s", excerpt(ended, `"events"`, 220))
	s.logf("engineer's next set: %v (must be ERR_GESTURE_ENDED)", foh.Apply(sendCmd("set", "sendLevel", 4, 2, "5", "foh")))
	_ = foh.Apply(sendCmd("holdEnd", "sendLevel", 4, 2, "", "foh"))
	s.logf("tap set (read-only in v0.4): %v", foh.Apply(sendCmd("set", "sendTap", 4, 1, `"postFader"`, "foh")))
}

func (s *busSession) monitorSeat(t *testing.T, sim *simx32.Sim, mon *Engine) {
	t.Helper()
	before := sim.WriteCount()
	for name, in := range map[string]string{
		"ch 1 channel fader":     cmd("fader", "-3", "gesture"),
		"ch 4 send to Mon 1":     sendCmd("set", "sendLevel", 4, 1, "0", "mon"),
		"Mon 3 mute":             busCmd("toggle", "mute", 3, "", "mon"),
		"show mode":              `{"version":0,"type":"setShowMode","value":true,"origin":{"kind":"gesture","clientId":"mon"}}`,
		"Mon 2 name (not a mix)": busCmd("set", "name", 2, `"Mine"`, "mon"),
	} {
		s.logf("monitor seat (bus 2) tries %s: %v", name, mon.Apply(in))
	}
	s.logf("console writes during the refusals: %d", sim.WriteCount()-before)
	_, _ = mon.Gesture(sendCmd("holdBegin", "sendLevel", 4, 2, "", "mon"))
	res, err := mon.Gesture(sendCmd("set", "sendLevel", 4, 2, "10", "mon"))
	s.logf("musician drags Keys in Mon 2 to +10 dB: %s %v (engineer's latest -10.0 + 6 dB)", res, err)
	res, err = mon.Gesture(busCmd("set", "fader", 2, "10", "mon"))
	s.logf("musician raises My level (bus 2 fader, engineer -3.0) to +10 dB: %s %v", res, err)
	time.Sleep(200 * time.Millisecond)
	for _, w := range sim.Writes()[before:] {
		s.logf("  console got %s %v from %s", w.Address, w.Arg.Float, w.From)
	}
}

func (s *busSession) auditJoin(t *testing.T, writes []simx32.Entry) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := len(writes) == len(s.audit)
	for i := 0; ok && i < len(writes); i++ {
		ok = writes[i].Address == s.audit[i].Address && s.audit[i].Origin.MayWrite()
	}
	s.logf("write audit: console %d writes, driver audited %d, every one a user gesture: %v", len(writes), len(s.audit), ok)
	if !ok {
		t.Fatal("write audit mismatch")
	}
	s.audit = nil
}

func (s *busSession) xair(t *testing.T) {
	sim := simxair.New(simxair.Options{Name: "XR18 (sim)"})
	_ = sim.SurfaceSet("/fxsend/1/config/name", osc.String("Reverb"))
	port := serveBusSim(t, sim.Serve)
	eng, st := s.engine(t, fmt.Sprintf(`{"version":0,"family":"xair","host":"127.0.0.1","port":%d}`, port))
	var m stateJSON
	_ = json.Unmarshal([]byte(st.waitFor(t, "snapshot", func(m string) bool { return strings.Contains(m, `"buses"`) })), &m)
	sd := m.Strips[0].Sends[1]
	s.logf("xair: %d buses (bus 7 %q kind %s), sendFields %+v, ch 1 send to bus 2: level %v on %v pan %v tap %q", len(m.Buses),
		m.Buses[6].Name, m.Buses[6].Kind, *m.SendFields, sd.LevelDB, deref(sd.On), derefF(sd.Pan), sd.Tap)
	s.logf("xair toggle sendOn: %v (no send on/off, XA section 3.3)", eng.Apply(sendCmd("toggle", "sendOn", 1, 2, "", "ui")))
	s.logf("xair send to FX 1 (bus 7) -10 dB: %v", eng.Apply(sendCmd("set", "sendLevel", 1, 7, "-10", "ui")))
	time.Sleep(100 * time.Millisecond)
	for _, w := range sim.Writes() {
		s.logf("  console got %s %v", w.Address, w.Arg.Float)
	}
}

func excerpt(s, key string, n int) string {
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	return s[i:min(len(s), i+n)]
}
