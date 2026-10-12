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
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// TestSimSessionXAir_Log runs an X Air session against sim/xair on real
// loopback UDP with the default (unscaled) timing and writes a timestamped
// log: connect and read, a write with its echo, meters, a snapshot load at
// the mixer, Wi-Fi drops and reconnects, and the ninth client limited. It
// takes about a minute and a half, so it runs only when
// MIXCORE_XAIR_SESSION_LOG names the log file:
//
//	MIXCORE_XAIR_SESSION_LOG=/tmp/xair.log go test -tags=sim -run TestSimSessionXAir_Log -v ./api/
func TestSimSessionXAir_Log(t *testing.T) {
	path := os.Getenv("MIXCORE_XAIR_SESSION_LOG")
	if path == "" {
		t.Skip("set MIXCORE_XAIR_SESSION_LOG to write the session log")
	}
	f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := newXAirSession(t, io.MultiWriter(f, testWriter{t}))
	s.readState(t)
	s.writeWithEcho(t)
	s.meters(t)
	s.snapshotLoad(t)
	s.wifiDrops(t)
	s.ninthClient(t)
	s.writesAudited(t)
}

// xairSession is one engine against one X Air simulator on loopback.
type xairSession struct {
	out   io.Writer
	start time.Time
	sim   *simxair.Sim
	port  int
	eng   *Engine
	mu    sync.Mutex
	msgs  []timedMsg
	raw   []string
	audit []driver.WriteRecord
}

func newXAirSession(t *testing.T, out io.Writer) *xairSession {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simxair.New(simxair.Options{Name: "XR18-1D-DA-B4", Model: "XR18", Firmware: "1.22", PushSnapIndex: true})
	s := &xairSession{out: out, start: time.Now(), sim: sim, port: pc.LocalAddr().(*net.UDPAddr).Port}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = s.sim.Serve(ctx, pc) })
	s.eng = NewEngineWith(Options{WriteAudit: func(w driver.WriteRecord) { s.mu.Lock(); s.audit = append(s.audit, w); s.mu.Unlock() }})
	s.eng.SetStateListener(s)
	t.Cleanup(func() { s.eng.Close(); cancel(); wg.Wait(); _ = pc.Close() })
	s.logf("sim/xair (XR18, firmware 1.22, PushSnapIndex on) on 127.0.0.1:%d, real UDP, default timing", s.port)
	for addr, a := range map[string]osc.Arg{"/ch/01/config/name": osc.String("Kick"), "/ch/02/config/name": osc.String("Vox"),
		"/ch/01/mix/fader": osc.Float(0.75), "/lr/mix/fader": osc.Float(0.75), "/ch/02/grp/mute": osc.Int(1)} {
		if err := s.sim.SurfaceSet(addr, a); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s *xairSession) OnState(js string) {
	var m stateJSON
	if json.Unmarshal([]byte(js), &m) != nil {
		return
	}
	s.mu.Lock()
	s.msgs = append(s.msgs, timedMsg{at: time.Now(), st: m})
	s.raw = append(s.raw, js)
	s.mu.Unlock()
}

func (s *xairSession) logf(format string, args ...any) {
	fmt.Fprintf(s.out, "[%8.3f s] %s\n", time.Since(s.start).Seconds(), fmt.Sprintf(format, args...))
}

func (s *xairSession) mustReach(t *testing.T, after time.Time, wait time.Duration, match func(stateJSON) bool, what string) timedMsg {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		msgs := s.msgs
		s.mu.Unlock()
		for _, m := range msgs {
			if m.at.After(after) && match(m.st) {
				return m
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no %s within %s", what, wait)
	return timedMsg{}
}

func (s *xairSession) readState(t *testing.T) {
	s.logf("== connect and read")
	t0 := time.Now()
	desc, err := s.eng.Start(fmt.Sprintf(`{"version":0,"family":"xair","host":"127.0.0.1","port":%d,"meters":{"inputs":true}}`, s.port))
	if err != nil {
		t.Fatal(err)
	}
	s.logf("Start -> %s", desc)
	live := s.mustReach(t, time.Time{}, 5*time.Second, func(m stateJSON) bool { return m.Status == "live" && m.Type == "snapshot" }, "live snapshot")
	st := live.st
	reads := 0
	for _, e := range s.sim.Log() {
		if e.Class == simxair.ClassRead {
			reads++
		}
	}
	main := st.Strips[len(st.Strips)-1]
	s.logf("live after %d ms: console %s %s firmware %s; %d input strips + main, %d mute groups, %d DCAs; %d reads, %d writes",
		live.at.Sub(t0).Milliseconds(), st.Console.Model, st.Console.Name, st.Console.Firmware, len(st.Strips)-1,
		len(st.MuteGroups), len(st.DCAs), reads, len(s.sim.Writes()))
	b, _ := json.Marshal(st.Strips[1])
	s.logf("input 2: %s", b)
	b, _ = json.Marshal(main)
	s.logf("main (no mute-group membership on X Air): %s", b)
	if len(st.Strips) != 17 || main.MuteGroups != nil || len(s.sim.Writes()) != 0 {
		t.Errorf("snapshot or writes wrong")
	}
}

func (s *xairSession) writeWithEcho(t *testing.T) {
	s.logf("== a write with its echo")
	t0 := time.Now()
	cmd := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":-10,"origin":{"kind":"gesture","clientId":"ui","seq":7}}`
	if err := s.eng.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	m := s.mustReach(t, t0, 2*time.Second, func(m stateJSON) bool { return changeOf(m, "input", 1, "fader") != nil }, "echo")
	v, _ := s.sim.Value("/ch/01/mix/fader")
	b, _ := json.Marshal(changeOf(m.st, "input", 1, "fader"))
	s.logf("Apply ch 1 fader -10 dB -> mixer /ch/01/mix/fader = %.7f (step %d); diff after %d ms: %s",
		v.Float, int(math.Round(float64(v.Float)*1023)), m.at.Sub(t0).Milliseconds(), b)
	time.Sleep(time.Second)
	n := 0
	for _, m := range s.since(t0) {
		if c := changeOf(m.st, "input", 1, "fader"); c != nil {
			n++
		}
	}
	s.logf("changes of ch 1 fader in the next second: %d (the echo once, no bounce)", n)
	if n != 1 {
		t.Errorf("echo seen %d times", n)
	}
}

func changeOf(m stateJSON, kind string, index int, field string) *changeJSON {
	if m.Changes == nil {
		return nil
	}
	for i, c := range *m.Changes {
		if c.Param.Strip.Kind == kind && c.Param.Strip.Index == index && c.Param.Field == field {
			return &(*m.Changes)[i]
		}
	}
	return nil
}

func (s *xairSession) since(t time.Time) []timedMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []timedMsg
	for _, m := range s.msgs {
		if m.at.After(t) {
			out = append(out, m)
		}
	}
	return out
}

func (s *xairSession) meters(t *testing.T) {
	s.logf("== meters (bank /meters/1, int16 in 1/256 dB)")
	s.sim.SetMeterValues([]int16{-4608, -1536, simxair.MeterSilence})
	s.sim.SetMeterValue(simxair.Bank1MainL, -256)
	s.sim.SetMeterValue(simxair.Bank1MainL+1, -512)
	time.Sleep(500 * time.Millisecond)
	buf := make([]byte, MeterFrameMaxBytes)
	seq0 := uint64(0)
	if n := s.eng.ReadMeterFrame(buf); n > 24 {
		seq0 = binary.LittleEndian.Uint64(buf[8:])
	}
	time.Sleep(time.Second)
	n := s.eng.ReadMeterFrame(buf)
	count := int(binary.LittleEndian.Uint16(buf[6:]))
	lv := func(i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(buf[24+4*i:])) }
	frames := binary.LittleEndian.Uint64(buf[8:]) - seq0
	s.logf("frame %d bytes, n = %d; input 1 %.2f, input 2 %.2f, input 3 %v dBFS; Main L %.2f, Main R %.2f dBFS; %d frames in 1 s",
		n, count, lv(0), lv(1), lv(2), lv(16), lv(17), frames)
	if count != 18 || lv(0) != -18 || lv(16) != -1 || lv(17) != -2 {
		t.Errorf("meter frame wrong")
	}
}

func (s *xairSession) snapshotLoad(t *testing.T) {
	s.logf("== snapshot 12 loaded at the mixer (by another app) while ch 16 is held")
	scene := map[string]osc.Arg{}
	for ch := 1; ch <= 16; ch++ {
		scene[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.5)
	}
	if err := s.sim.StoreSnapshot(12, "Sunday", scene); err != nil {
		t.Fatal(err)
	}
	hold := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":16},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`
	if err := s.eng.Apply(hold); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	if err := s.sim.LoadSnapshot(12); err != nil {
		t.Fatal(err)
	}
	m := s.mustReach(t, t0, 2*time.Second, func(m stateJSON) bool { return hasEvent(m, "sceneRecalledAtConsole") }, "scene event")
	for _, ev := range m.st.Events {
		b, _ := json.Marshal(ev)
		s.logf("event after %d ms: %s", m.at.Sub(t0).Milliseconds(), b)
	}
	err := s.eng.Apply(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":16},"field":"fader"},"value":-5,"origin":{"kind":"gesture","clientId":"ui"}}`)
	s.logf("Apply to the held ch 16 after the load -> %s", CodeOf(err))
	s.mu.Lock()
	joined := strings.Join(s.raw, "\n")
	s.mu.Unlock()
	if CodeOf(err) != CodeGestureEnded || !strings.Contains(joined, `"loadedScene":12`) {
		t.Errorf("the load must end the hold and name snapshot 12: %v", err)
	}
	_ = s.eng.Apply(`{"version":0,"type":"holdEnd","param":{"strip":{"kind":"input","index":16},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`)
}

const xairDropRounds = 10

func (s *xairSession) wifiDrops(t *testing.T) {
	s.logf("== %d Wi-Fi drops (mixer unreachable 2 s each, meters flowing)", xairDropRounds)
	clock := &meterClock{}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { clock.run(ctx, s.eng) })
	defer func() { cancel(); wg.Wait() }()
	worst, best := time.Duration(0), time.Hour
	for i := range xairDropRounds {
		time.Sleep(500 * time.Millisecond)
		before, _ := s.sim.Value("/ch/03/mix/fader")
		s.sim.Pause()
		cut := time.Now()
		stale := s.mustReach(t, cut, 5*time.Second, func(m stateJSON) bool { return m.Status == "stale" }, "stale")
		lat := stale.at.Sub(clock.at())
		worst, best = max(worst, lat), min(best, lat)
		applyErr := s.eng.Apply(muteCmd(3, true))
		time.Sleep(time.Until(cut.Add(2 * time.Second)))
		s.sim.Resume()
		back := time.Now()
		live := s.mustReach(t, back, 10*time.Second, func(m stateJSON) bool { return m.Status == "live" }, "live")
		after, _ := s.sim.Value("/ch/03/mix/fader")
		s.logf("drop %2d: stale %4d ms after the last packet, Apply while stale -> %s, live %4d ms after the link returned, mixer unchanged: %t",
			i+1, lat.Milliseconds(), CodeOf(applyErr), live.at.Sub(back).Milliseconds(), before.Equal(after))
		if lat < staleTarget-staleTolerance || lat > staleTarget+staleTolerance || CodeOf(applyErr) != CodeStale {
			t.Errorf("drop %d: stale after %s, apply %v", i+1, lat, applyErr)
		}
	}
	use := measureXAirSlots(s.sim)
	s.logf("stale latency over %d drops: min %d ms, max %d ms (target 1500 +- 200)", xairDropRounds, best.Milliseconds(), worst.Milliseconds())
	s.logf("mixer client table: %d source port(s) seen, %d sent /xremote, at most %d slot(s) in use at once",
		use.ports, use.xremotePorts, use.maxSlots)
}

func measureXAirSlots(sim *simxair.Sim) slotUse {
	ports, xr := map[netip.AddrPort]bool{}, map[netip.AddrPort]bool{}
	for _, e := range sim.Log() {
		ports[e.From] = true
		if e.Address == "/xremote" {
			xr[e.From] = true
		}
	}
	return slotUse{ports: len(ports), xremotePorts: len(xr), maxSlots: sim.MaxSlotsUsed()}
}

func (s *xairSession) ninthClient(t *testing.T) {
	s.logf("== client limit: eight other apps take the eight /xremote slots while we are paused (XA-F21)")
	_ = s.eng.Pause()
	time.Sleep(simxair.DefaultSubscriptionTTL + time.Second)
	stop := s.otherApps(t, 8)
	t0 := time.Now()
	if err := s.eng.Resume(); err != nil {
		t.Fatal(err)
	}
	s.mustReach(t, t0, 5*time.Second, func(m stateJSON) bool { return m.Status == "live" }, "live")
	s.logf("live again; subscribers: %d, all held by the other apps", len(s.sim.Subscribers()))
	first := time.Now()
	for ch := 5; ch <= 7; ch++ {
		_ = s.eng.Apply(muteCmd(ch, true))
		time.Sleep(100 * time.Millisecond)
	}
	lim := s.mustReach(t, first, 5*time.Second, func(m stateJSON) bool { return m.Limited }, "limited")
	s.logf("limited=%t reason=%s status=%s, %d ms after the first unconfirmed write", lim.st.Limited, lim.st.LimitedReason,
		lim.st.Status, lim.at.Sub(first).Milliseconds())
	s.logf("Apply while limited -> %s", CodeOf(s.eng.Apply(muteCmd(8, true))))
	stop()
	s.logf("other apps closed; waiting for their slots to expire")
	time.Sleep(14 * time.Second)
	t1 := time.Now()
	_ = s.sim.SurfaceSet("/ch/09/mix/on", osc.Int(0)) // another app changes something
	clr := s.mustReach(t, t1, 3*time.Second, func(m stateJSON) bool { return !m.Limited }, "limit cleared")
	s.logf("a change by another app arrived: limited=%t after %d ms", clr.st.Limited, clr.at.Sub(t1).Milliseconds())
	for ch := 5; ch <= 7; ch++ {
		_ = s.eng.Apply(muteCmd(ch, false))
	}
}

func (s *xairSession) otherApps(t *testing.T, n int) func() {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	to := net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(s.port)))
	pkt, _ := osc.AppendString(nil, "/xremote")
	for range n {
		pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			defer pc.Close()
			tick := time.NewTicker(3 * time.Second)
			defer tick.Stop()
			for {
				_, _ = pc.WriteTo(pkt, to)
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		})
	}
	time.Sleep(50 * time.Millisecond)
	return func() { cancel(); wg.Wait() }
}

func (s *xairSession) writesAudited(t *testing.T) {
	time.Sleep(300 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	writes := s.sim.Writes()
	gestures, snaps := 0, 0
	for _, a := range s.audit {
		if a.Origin.MayWrite() {
			gestures++
		}
	}
	for _, e := range s.sim.Log() {
		if strings.HasPrefix(e.Address, "/-snap/") {
			snaps++
		}
	}
	s.logf("== mixer received %d writes; the engine audited %d, all with a gesture origin: %t; packets to /-snap/ addresses: %d",
		len(writes), len(s.audit), gestures == len(s.audit), snaps)
	if len(writes) != len(s.audit) || gestures != len(s.audit) || snaps != 0 {
		t.Errorf("writes %d, audit %d, gestures %d, snapshot packets %d", len(writes), len(s.audit), gestures, snaps)
	}
}
