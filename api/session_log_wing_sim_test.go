// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
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
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestSimSessionWing runs a WING session through the whole API against
// sim/wing on real loopback TCP and UDP with the default timing: connect
// and read, a fader and a Gain write settled by read-back, meters, a desk
// change, a TCP remote lock, a dropped connection and the reconnect,
// discovery, and the write-audit join. The log goes to the test output and,
// when MIXCORE_WING_SESSION_LOG names a file, to that file:
//
//	MIXCORE_WING_SESSION_LOG=/tmp/wing.log go test -tags=sim -run TestSimSessionWing -v ./api/
func TestSimSessionWing(t *testing.T) {
	var out io.Writer = testWriter{t}
	if path := os.Getenv("MIXCORE_WING_SESSION_LOG"); path != "" {
		f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = io.MultiWriter(f, out)
	}
	s := newWingSession(t, out)
	s.readState(t)
	s.faderWrite(t)
	s.gainWrite(t)
	s.meters(t)
	s.deskChange(t)
	s.remoteLock(t)
	s.dropAndReconnect(t)
	s.discovery(t)
	s.writesAudited(t)
}

type wingSession struct {
	xairSession // log, listener and wait helpers
	wsim        *simwing.Sim
	udpPort     int
	disc        chan string
}

func newWingSession(t *testing.T, out io.Writer) *wingSession {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simwing.New(simwing.Options{Name: "FOH", Model: "wing-compact", Firmware: "3.1.0-0-g0c2b9d4a:release", IP: "127.0.0.1"})
	s := &wingSession{wsim: sim, udpPort: pc.LocalAddr().(*net.UDPAddr).Port, disc: make(chan string, 1)}
	s.out, s.start, s.port = out, time.Now(), ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, ln) })
	wg.Go(func() { _ = sim.ServeUDP(ctx, pc) })
	s.eng = NewEngineWith(Options{WriteAudit: func(w driver.WriteRecord) { s.mu.Lock(); s.audit = append(s.audit, w); s.mu.Unlock() }})
	s.eng.SetStateListener(s)
	s.eng.SetDiscoveryListener(s)
	t.Cleanup(func() { s.eng.Close(); cancel(); wg.Wait(); _ = pc.Close() })
	s.logf("sim/wing (wing-compact, firmware 3.1.0) on 127.0.0.1 TCP %d, UDP %d; real sockets, default timing", s.port, s.udpPort)
	for path, v := range map[string]any{"/ch/1/name": "Kick", "/ch/2/name": "Vox", "/ch/2/tags": "#D1", "/ch/1/fdr": 0.0,
		"/main/1/fdr": 0.0, "/mgrp/3/mute": 1, "/io/in/LCL/1/g": 20.0} {
		if err := sim.SurfaceSet(path, v); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s *wingSession) OnDiscovery(js string) { s.disc <- js }

func (s *wingSession) readState(t *testing.T) {
	s.logf("== connect: schema walk (definition requests only) and initial read")
	t0 := time.Now()
	desc, err := s.eng.Start(fmt.Sprintf(`{"version":0,"family":"wing","host":"127.0.0.1","port":%d,"meters":{"inputs":true}}`, s.port))
	if err != nil {
		t.Fatal(err)
	}
	s.logf("Start -> %s", desc)
	live := s.mustReach(t, time.Time{}, 15*time.Second, func(m stateJSON) bool { return m.Status == "live" && m.Type == "snapshot" }, "live snapshot")
	st := live.st
	defs, reads := countWing(s.wsim, simwing.ClassDefs), countWing(s.wsim, simwing.ClassRead)
	s.logf("live after %d ms: %s %s firmware %s; %d inputs + main, %d mute groups, %d DCAs, %d headamps; %d definition requests, %d reads, %d writes",
		live.at.Sub(t0).Milliseconds(), st.Console.Model, st.Console.Name, st.Console.Firmware, len(st.Strips)-1,
		len(st.MuteGroups), len(st.DCAs), len(st.Headamps), defs, reads, len(s.wsim.Writes()))
	b, _ := json.Marshal(st.Strips[1])
	s.logf("input 2: %s", b)
	if len(st.Strips) != 41 || len(st.MuteGroups) != 8 || len(st.DCAs) != 16 || len(st.Headamps) != 24 || len(s.wsim.Writes()) != 0 {
		t.Errorf("snapshot or writes wrong: %d strips, %d headamps, %d writes", len(st.Strips), len(st.Headamps), len(s.wsim.Writes()))
	}
	if !strings.Contains(desc, `"faderLaw":"wing"`) {
		t.Errorf("session %s", desc)
	}
}

func (s *wingSession) faderWrite(t *testing.T) {
	s.logf("== a fader write; the console does not echo (sim default, item 4): settled by a read-back")
	t0 := time.Now()
	cmd := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":-10,"origin":{"kind":"gesture","clientId":"ui","seq":7}}`
	if err := s.eng.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	m := s.mustReach(t, t0, 2*time.Second, func(m stateJSON) bool { return changeOf(m, "input", 1, "fader") != nil }, "read-back")
	v, _ := s.wsim.Value("/ch/1/fdr")
	b, _ := json.Marshal(changeOf(m.st, "input", 1, "fader"))
	s.logf("Apply ch 1 fader -10 dB -> console /ch/1/fdr = %v dB; diff after %d ms: %s", v, m.at.Sub(t0).Milliseconds(), b)
	time.Sleep(time.Second)
	n := 0
	for _, m := range s.since(t0) {
		if changeOf(m.st, "input", 1, "fader") != nil {
			n++
		}
	}
	s.logf("changes of ch 1 fader within 1 s: %d (one read-back, no bounce)", n)
	if v != -10.0 || n != 1 {
		t.Errorf("fader %v, %d changes", v, n)
	}
}

func (s *wingSession) gainWrite(t *testing.T) {
	s.logf("== a Gain write on local 1 (law from the console: -2.5..+45 dB, 2.5 dB steps)")
	t0 := time.Now()
	cmd := `{"version":0,"type":"set","param":{"strip":{"kind":"headamp","index":1},"field":"gain"},"value":22.5,"origin":{"kind":"gesture","clientId":"ui","seq":8}}`
	if err := s.eng.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	m := s.mustReach(t, t0, 2*time.Second, func(m stateJSON) bool { return changeOf(m, "headamp", 1, "gain") != nil }, "gain read-back")
	v, _ := s.wsim.Value("/io/in/LCL/1/g")
	s.logf("console /io/in/LCL/1/g = %v dB after %d ms", v, m.at.Sub(t0).Milliseconds())
	if v != 22.5 {
		t.Errorf("gain %v", v)
	}
}

func (s *wingSession) meters(t *testing.T) {
	s.logf("== meters (UDP to our port, renewed every 1.5 s of the 5 s lifetime)")
	s.wsim.SetInputLevel(1, -18)
	s.wsim.SetMainLevels(-9, -9.5)
	time.Sleep(7 * time.Second)
	f := s.eng.MeterFrame()
	if len(f) < 24 {
		t.Fatalf("no meter frame after 7 s")
	}
	n := int(binary.LittleEndian.Uint16(f[6:8]))
	lvl := func(i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(f[24+4*i:])) }
	s.logf("frame after 7 s: n = %d, input 1 %.1f dBFS, input 2 %.1f, Main L %.1f, Main R %.1f", n, lvl(0), lvl(1), lvl(n-2), lvl(n-1))
	if n != 42 || lvl(0) != -18 || lvl(n-2) != -9 || lvl(n-1) != -9.5 {
		t.Errorf("meter frame wrong")
	}
}

func (s *wingSession) deskChange(t *testing.T) {
	s.logf("== a change at the console")
	t0 := time.Now()
	if err := s.wsim.SurfaceSet("/ch/2/mute", 1); err != nil {
		t.Fatal(err)
	}
	m := s.mustReach(t, t0, time.Second, func(m stateJSON) bool { return changeOf(m, "input", 2, "mute") != nil }, "desk mute")
	b, _ := json.Marshal(changeOf(m.st, "input", 2, "mute"))
	s.logf("ch 2 muted at the desk -> diff after %d ms: %s", m.at.Sub(t0).Milliseconds(), b)
}

func (s *wingSession) remoteLock(t *testing.T) {
	s.logf("== TCP remote lock on at the console (WG-F26)")
	_ = s.wsim.SurfaceSet("/$syscfg/tcplock", 1)
	time.Sleep(200 * time.Millisecond)
	err := s.eng.Apply(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":3},"field":"fader"},"value":-5,"origin":{"kind":"gesture","clientId":"ui","seq":9}}`)
	s.logf("Apply while locked -> %v", err)
	if err == nil || !strings.Contains(err.Error(), CodeUnsupported) {
		t.Errorf("locked write: %v", err)
	}
	_ = s.wsim.SurfaceSet("/$syscfg/tcplock", 0)
	time.Sleep(200 * time.Millisecond)
}

func (s *wingSession) dropAndReconnect(t *testing.T) {
	s.logf("== the console drops every TCP connection; ch 4 changes while we are away")
	t0 := time.Now()
	s.wsim.DropConnections()
	_ = s.wsim.SurfaceSet("/ch/4/fdr", -7.5)
	stale := s.mustReach(t, t0, 3*time.Second, func(m stateJSON) bool { return m.Status == "stale" }, "stale")
	err := s.eng.Apply(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":5},"field":"fader"},"value":-3,"origin":{"kind":"gesture","clientId":"ui","seq":10}}`)
	s.logf("stale after %d ms (%s); Apply while stale -> %v", stale.at.Sub(t0).Milliseconds(), stale.st.Reason, err)
	live := s.mustReach(t, stale.at, 5*time.Second, func(m stateJSON) bool { return m.Status == "live" && m.Type == "snapshot" }, "live again")
	s.logf("live again %d ms after the drop; schema reused (definition requests still %d); ch 4 fader read from the console: %v dB",
		live.at.Sub(t0).Milliseconds(), countWing(s.wsim, simwing.ClassDefs), live.st.Strips[3].FaderDB)
	if v, _ := s.wsim.Value("/ch/5/fdr"); v != -144.0 || err == nil {
		t.Errorf("a write while stale reached the console or was accepted: %v %v", v, err)
	}
}

func (s *wingSession) discovery(t *testing.T) {
	s.logf("== discovery: WING? to UDP %d", s.udpPort)
	if err := s.eng.Discover(fmt.Sprintf(`{"version":0,"family":"wing","hosts":[{"host":"127.0.0.1","port":%d}],"waitMs":500}`, s.udpPort)); err != nil {
		t.Fatal(err)
	}
	select {
	case js := <-s.disc:
		s.logf("discovery -> %s", js)
		if !strings.Contains(js, `"family":"wing"`) || !strings.Contains(js, `"model":"wing-compact"`) {
			t.Errorf("discovery %s", js)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no discovery result")
	}
}

func (s *wingSession) writesAudited(t *testing.T) {
	s.mu.Lock()
	audit := append([]driver.WriteRecord(nil), s.audit...)
	s.mu.Unlock()
	writes := s.wsim.Writes()
	s.logf("== write audit: %d writes at the console, %d audited by the driver", len(writes), len(audit))
	for i, w := range writes {
		s.logf("  console write %d: %s %s applied %v", i+1, w.Path, w.Value, w.Applied)
	}
	if len(writes) != len(audit) {
		t.Errorf("write audit join: %d console writes, %d audited", len(writes), len(audit))
	}
	for i := range min(len(writes), len(audit)) {
		if writes[i].Path != audit[i].Address || !audit[i].Origin.MayWrite() {
			t.Errorf("write %d: console %s, audit %+v", i, writes[i].Path, audit[i])
		}
	}
}

func countWing(s *simwing.Sim, c simwing.Class) int {
	n := 0
	for _, e := range s.Log() {
		if e.Class == c {
			n++
		}
	}
	return n
}
