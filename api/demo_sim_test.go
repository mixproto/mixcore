// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// TestDemoSim_Session runs the demo button's journey on real loopback UDP
// with the built-in drivers and default timing, for both families, and logs
// it. Set MIXCORE_DEMO_SESSION_LOG to also write the log to a file:
//
//	MIXCORE_DEMO_SESSION_LOG=/tmp/demo.log go test -tags=sim -run TestDemoSim_Session -v ./api/
func TestDemoSim_Session(t *testing.T) {
	out := io.Writer(testWriter{t})
	if path := os.Getenv("MIXCORE_DEMO_SESSION_LOG"); path != "" {
		f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = io.MultiWriter(f, out)
	}
	for _, fam := range []string{familyX32, familyXAir} {
		t.Run(fam, func(t *testing.T) { runDemoSession(t, out, fam) })
	}
}

// refuseAll is an Android socket binder with no network to bind to.
type refuseAll struct{}

func (refuseAll) BindSocket(int) error { return errors.New("no network") }

type demoSession struct {
	t     *testing.T
	out   io.Writer
	start time.Time
	eng   *Engine
	mu    sync.Mutex
	msgs  []timedMsg
	raw   []string
	audit []driver.WriteRecord
}

func (s *demoSession) OnState(js string) {
	var m stateJSON
	if json.Unmarshal([]byte(js), &m) != nil {
		return
	}
	s.mu.Lock()
	s.msgs, s.raw = append(s.msgs, timedMsg{at: time.Now(), st: m}), append(s.raw, js)
	s.mu.Unlock()
}

func (s *demoSession) logf(format string, args ...any) {
	fmt.Fprintf(s.out, "[%7.3f s] %s\n", time.Since(s.start).Seconds(), fmt.Sprintf(format, args...))
}

func (s *demoSession) wait(what string, match func(stateJSON) bool) timedMsg {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		msgs := s.msgs
		s.mu.Unlock()
		for _, m := range msgs {
			if match(m.st) {
				return m
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.t.Fatalf("no %s within 5 s", what)
	return timedMsg{}
}

func runDemoSession(t *testing.T, out io.Writer, fam string) {
	s := &demoSession{t: t, out: out, start: time.Now()}
	s.eng = NewEngineWith(Options{WriteAudit: func(w driver.WriteRecord) { s.mu.Lock(); s.audit = append(s.audit, w); s.mu.Unlock() }})
	defer s.eng.Close()
	s.eng.SetStateListener(s)
	s.eng.SetSocketBinder(refuseAll{})
	s.logf("== %s demo; the socket binder refuses every socket (Android with no network)", fam)
	res, err := s.eng.StartDemo(`{"version":0,"family":"` + fam + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	var m demoMixerJSON
	_ = json.Unmarshal([]byte(res), &m)
	s.logf("StartDemo -> %s", res)
	t0 := time.Now()
	desc, err := s.eng.Start(demoConfig(m))
	if err != nil || !strings.Contains(desc, `"demo":true`) {
		t.Fatalf("Start: %s %v", desc, err)
	}
	s.logf("Start -> %s", desc)
	s.connected(t0)
	s.gesture()
	s.safetyPath()
	s.meters(fam)
	s.discovery(fam, m)
	s.stop(m)
}

func (s *demoSession) connected(t0 time.Time) {
	live := s.wait("live snapshot", func(m stateJSON) bool { return m.Status == "live" && m.Type == "snapshot" })
	st := live.st
	names := make([]string, 0, len(st.Strips))
	for _, sp := range st.Strips {
		names = append(names, sp.Name)
	}
	s.logf("live after %d ms, demo=%v, console %s %q: %d strips: %s", live.at.Sub(t0).Milliseconds(), st.Demo,
		st.Console.Model, st.Console.Name, len(st.Strips), strings.Join(names, ", "))
	pastor, _ := json.Marshal(st.Strips[12])
	s.logf("input 13: %s", pastor)
	s.logf("mute groups %v, DCAs %v", st.MuteGroups, st.DCAs)
	if !st.Demo || st.Strips[12].EffectiveMute == nil || !mutedTrue(st.Strips[12].EffectiveMute) {
		s.t.Fatalf("snapshot %+v", st)
	}
}

func (s *demoSession) gesture() {
	t0 := time.Now()
	if err := s.eng.Apply(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":10},"field":"fader"},` +
		`"value":-12,"origin":{"kind":"gesture","clientId":"ui","seq":1}}`); err != nil {
		s.t.Fatal(err)
	}
	echo := s.wait("echo", func(m stateJSON) bool { return changeOf(m, "input", 10, "fader") != nil })
	b, _ := json.Marshal(changeOf(echo.st, "input", 10, "fader"))
	s.logf("Apply input 10 (Vox 1) fader -12 dB -> echo after %d ms: %s", echo.at.Sub(t0).Milliseconds(), b)
	if c := changeOf(echo.st, "input", 10, "fader"); c.Origin.ClientID != "ui" || math.Abs(c.Value.(float64)+12) > 0.1 {
		s.t.Fatalf("echo %+v", c)
	}
}

func (s *demoSession) meters(fam string) {
	lv := func(f []byte, i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(f[24+4*i:])) }
	var first, last []byte
	for k := range 4 {
		time.Sleep(250 * time.Millisecond)
		f := s.eng.MeterFrame()
		n := int(binary.LittleEndian.Uint16(f[6:]))
		s.logf("meter frame seq %d n=%d: Kick %6.1f  Snare %6.1f  Hi-Hat %6.1f  Bass %6.1f  Vox 1 %6.1f  Pastor %6.1f  Main L %6.1f  R %6.1f dBFS",
			binary.LittleEndian.Uint64(f[8:]), n, lv(f, 0), lv(f, 1), lv(f, 2), lv(f, 5), lv(f, 9), lv(f, 12), lv(f, n-2), lv(f, n-1))
		if k == 0 {
			first = f
		}
		last = f
	}
	if n := int(binary.LittleEndian.Uint16(last[6:])); n != map[string]int{familyX32: 34, familyXAir: 18}[fam] ||
		lv(first, 0) == lv(last, 0) || lv(last, n-2) < -40 {
		s.t.Fatal("meters must follow the family map, move and carry Main L/R")
	}
}

// discovery probes the demo's own address next to a plain simulator on
// loopback: only the simulator is listed.
func (s *demoSession) discovery(fam string, m demoMixerJSON) {
	s.eng.SetSocketBinder(nil) // discovery uses the app's network path
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }() // cancel, then wait
	if fam == familyXAir {
		sim := simxair.New(simxair.Options{})
		wg.Go(func() { _ = sim.Serve(ctx, pc) })
	} else {
		sim := simx32.New(simx32.Options{})
		wg.Go(func() { _ = sim.Serve(ctx, pc) })
	}
	other := pc.LocalAddr().(*net.UDPAddr).Port
	got := make(chan string, 1)
	s.eng.SetDiscoveryListener(discoveryFunc(func(r string) { got <- r }))
	req := fmt.Sprintf(`{"version":0,"family":%q,"hosts":[{"host":"127.0.0.1","port":%d},{"host":"127.0.0.1","port":%d}],"waitMs":500}`,
		fam, m.Port, other)
	if err := s.eng.Discover(req); err != nil {
		s.t.Fatal(err)
	}
	r := <-got
	s.logf("Discover demo port %d and a plain simulator on port %d -> %s", m.Port, other, r)
	if strings.Contains(r, fmt.Sprintf(`"port":%d`, m.Port)) || !strings.Contains(r, fmt.Sprintf(`"port":%d`, other)) {
		s.t.Fatal("discovery must list the plain simulator and never the demo")
	}
}

type discoveryFunc func(string)

func (f discoveryFunc) OnDiscovery(r string) { f(r) }

func (s *demoSession) stop(m demoMixerJSON) {
	s.mu.Lock()
	writes, raw := len(s.audit), s.raw
	s.mu.Unlock()
	for _, js := range raw {
		if !strings.Contains(js, `"demo":true`) {
			s.t.Fatalf("message without demo: %s", js)
		}
	}
	t0 := time.Now()
	if err := s.eng.StopDemo(); err != nil {
		s.t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", m.Port))
	if err != nil {
		s.t.Fatalf("demo port still held: %v", err)
	}
	_ = pc.Close()
	s.logf("StopDemo in %d ms; session %q; port %d free again; %d state messages, all demo=true; writes on the wire: %d (the three gestures)",
		time.Since(t0).Milliseconds(), s.msgs[len(s.msgs)-1].st.Status, m.Port, len(raw), writes)
	if writes != 3 {
		s.t.Fatalf("%d writes, want only the user's three gestures", writes)
	}
}

// safetyPath shows the demo session in the console layer's safety path:
// slip-guard wall on a fader, Gain cap on a headamp, through Gesture.
func (s *demoSession) safetyPath() {
	for _, c := range []struct{ typ, param, extra string }{
		{"holdBegin", demoVox1Fader, ""}, {"set", demoVox1Fader, `,"value":10`}, {"holdEnd", demoVox1Fader, ""},
		{"holdBegin", demoVox1Gain, ""}, {"set", demoVox1Gain, `,"value":59`}, {"holdEnd", demoVox1Gain, ""},
	} {
		got, err := s.eng.Gesture(cmdJSON(c.typ, c.param, c.extra))
		if err != nil {
			s.t.Fatalf("%s: %v", c.typ, err)
		}
		s.logf("Gesture %s %s%s -> %s", c.typ, c.param, c.extra, got)
		if c.typ == "set" && !strings.Contains(got, `"state":"atWall"`) && !strings.Contains(got, `"state":"atCap"`) {
			s.t.Fatalf("the demo session must stop at the wall or the cap: %s", got)
		}
	}
	echo := s.wait("Gain echo", func(m stateJSON) bool { return changeOf(m, "headamp", 10, "gain") != nil })
	b, _ := json.Marshal(changeOf(echo.st, "headamp", 10, "gain"))
	s.logf("Gain echo: %s", b)
}
