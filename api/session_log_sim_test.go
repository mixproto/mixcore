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
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// TestSimSession_Log runs the behaviour of contract v0.1 against the
// simulator on real loopback UDP with the default (unscaled) timing and
// writes a timestamped session log. It takes about two minutes, so it runs
// only when MIXCORE_SESSION_LOG names the log file:
//
//	MIXCORE_SESSION_LOG=/tmp/session.log go test -tags=sim -run TestSimSession_Log -v ./api/
func TestSimSession_Log(t *testing.T) {
	path := os.Getenv("MIXCORE_SESSION_LOG")
	if path == "" {
		t.Skip("set MIXCORE_SESSION_LOG to write the session log")
	}
	f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := newSession(t, io.MultiWriter(f, testWriter{t}))
	s.reconnects(t)
	s.limited(t)
	s.muteGroupToggle(t)
	s.sceneRecallEndsHold(t)
	s.writesAudited(t)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) { w.t.Log(string(b[:len(b)-1])); return len(b), nil }

type timedMsg struct {
	at time.Time
	st stateJSON
}

// session is one engine against one simulator on loopback.
type session struct {
	out   io.Writer
	start time.Time
	sim   *simx32.Sim
	port  int
	eng   *Engine
	mu    sync.Mutex
	msgs  []timedMsg
	audit []driver.WriteRecord
}

func newSession(t *testing.T, out io.Writer) *session {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &session{out: out, start: time.Now(), sim: simx32.New(simx32.Options{Name: "X32-SIM"}), port: pc.LocalAddr().(*net.UDPAddr).Port}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = s.sim.Serve(ctx, pc) })
	s.eng = NewEngineWith(Options{WriteAudit: func(w driver.WriteRecord) { s.mu.Lock(); s.audit = append(s.audit, w); s.mu.Unlock() }})
	s.eng.SetStateListener(s)
	t.Cleanup(func() { s.eng.Close(); cancel(); wg.Wait(); _ = pc.Close() })
	s.logf("mixsim (sim/x32) on 127.0.0.1:%d, real UDP, default timing; engine meters on", s.port)
	if _, err := s.eng.Start(fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d,"meters":{"inputs":true}}`, s.port)); err != nil {
		t.Fatal(err)
	}
	s.mustReach(t, time.Time{}, 5*time.Second, func(m stateJSON) bool { return m.Status == "live" }, "live")
	return s
}

func (s *session) OnState(js string) {
	var m stateJSON
	if json.Unmarshal([]byte(js), &m) != nil {
		return
	}
	s.mu.Lock()
	s.msgs = append(s.msgs, timedMsg{at: time.Now(), st: m})
	s.mu.Unlock()
}

func (s *session) logf(format string, args ...any) {
	fmt.Fprintf(s.out, "[%8.3f s] %s\n", time.Since(s.start).Seconds(), fmt.Sprintf(format, args...))
}

// mustReach waits for a message after t that matches and returns its time.
func (s *session) mustReach(t *testing.T, after time.Time, wait time.Duration, match func(stateJSON) bool, what string) timedMsg {
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

// meterClock remembers the capture time of the newest meter frame; it samples
// continuously because the engine empties the frame as soon as it goes stale.
type meterClock struct {
	mu   sync.Mutex
	last time.Time
}

func (c *meterClock) run(ctx context.Context, eng *Engine) {
	buf := make([]byte, MeterFrameMaxBytes)
	for ctx.Err() == nil {
		if n := eng.ReadMeterFrame(buf); n > meters.FrameHeaderSize {
			if ns := int64(binary.LittleEndian.Uint64(buf[16:])); ns != 0 {
				c.mu.Lock()
				c.last = time.Unix(0, ns)
				c.mu.Unlock()
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func (c *meterClock) at() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

const (
	logRounds      = 20
	staleTarget    = 1500 * time.Millisecond
	staleTolerance = 200 * time.Millisecond
)

// reconnects: 20 network outages; stale 1.5 s after the last packet; one slot.
func (s *session) reconnects(t *testing.T) {
	s.logf("== 20 outages (simulator unreachable 2 s each)")
	clock := &meterClock{}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { clock.run(ctx, s.eng) })
	defer func() { cancel(); wg.Wait() }()
	worst, best := time.Duration(0), time.Hour
	for i := range logRounds {
		time.Sleep(500 * time.Millisecond) // meters flowing
		s.sim.Pause()
		cut := time.Now()
		stale := s.mustReach(t, cut, 5*time.Second, func(m stateJSON) bool { return m.Status == "stale" }, "stale")
		lat := stale.at.Sub(clock.at())
		worst, best = max(worst, lat), min(best, lat)
		time.Sleep(time.Until(cut.Add(2 * time.Second)))
		s.sim.Resume()
		back := time.Now()
		live := s.mustReach(t, back, 10*time.Second, func(m stateJSON) bool { return m.Status == "live" }, "live")
		s.logf("round %2d: stale %4d ms after the last packet (staleSinceMs %d), live %4d ms after the link returned",
			i+1, lat.Milliseconds(), stale.st.StaleSinceMs, live.at.Sub(back).Milliseconds())
		if lat < staleTarget-staleTolerance || lat > staleTarget+staleTolerance {
			t.Errorf("round %d: stale after %s, want %s +- %s", i+1, lat, staleTarget, staleTolerance)
		}
	}
	use := measureSlots(s.sim)
	s.logf("stale latency over %d rounds: min %d ms, max %d ms (target 1500 +- 200)", logRounds, best.Milliseconds(), worst.Milliseconds())
	s.logf("console client table: %d source port(s) seen, %d sent /xremote, at most %d slot(s) in use at once",
		use.ports, use.xremotePorts, use.maxSlots)
	if use.xremotePorts != 1 || use.maxSlots != 1 {
		t.Errorf("slots: %+v, want one /xremote port and one slot", use)
	}
}

// limited: four other apps hold the slots; our writes get no echo.
func (s *session) limited(t *testing.T) {
	s.logf("== client limit: four other apps take the four /xremote slots while we are paused")
	_ = s.eng.Pause()                                       // stop renewing (the sockets stay)
	time.Sleep(simx32.DefaultSubscriptionTTL + time.Second) // our registration expires
	stop := s.otherApps(t)
	t0 := time.Now()
	if err := s.eng.Resume(); err != nil {
		t.Fatal(err)
	}
	s.mustReach(t, t0, 5*time.Second, func(m stateJSON) bool { return m.Status == "live" }, "live")
	s.logf("live again; slots in use: %d, all four held by the other apps: %t", len(s.sim.Slots()), len(s.sim.Subscribers()) == 4)
	first := time.Now()
	for ch := 5; ch <= 7; ch++ {
		_ = s.eng.Apply(muteCmd(ch, true))
		time.Sleep(100 * time.Millisecond)
	}
	lim := s.mustReach(t, first, 5*time.Second, func(m stateJSON) bool { return m.Limited }, "limited")
	s.logf("limited=%t reason=%s status=%s, %d ms after the first unconfirmed write", lim.st.Limited, lim.st.LimitedReason,
		lim.st.Status, lim.at.Sub(first).Milliseconds())
	err := s.eng.Apply(muteCmd(8, true))
	s.logf("Apply while limited -> %s", CodeOf(err))
	stop()
	s.logf("other apps closed; waiting for their slots to expire")
	time.Sleep(14 * time.Second)
	t1 := time.Now()
	_ = s.sim.SurfaceSet("/ch/09/mix/on", osc.Int(0))
	clr := s.mustReach(t, t1, 3*time.Second, func(m stateJSON) bool { return !m.Limited }, "limit cleared")
	s.logf("a desk change arrived: limited=%t after %d ms", clr.st.Limited, clr.at.Sub(t1).Milliseconds())
	for ch := 5; ch <= 7; ch++ { // restore the channels the limited writes muted
		_ = s.eng.Apply(muteCmd(ch, false))
	}
}

func (s *session) otherApps(t *testing.T) func() {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	to := net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(s.port)))
	pkt, _ := osc.AppendString(nil, "/xremote")
	for range 4 {
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

// muteGroupToggle: ch 5 is in mute group 2; a toggle engages the group.
func (s *session) muteGroupToggle(t *testing.T) {
	s.logf("== mute group toggle with effective mute (ch 5 is a member of mute group 2)")
	_ = s.sim.SurfaceSet("/ch/05/grp/mute", osc.Int(1<<1))
	time.Sleep(200 * time.Millisecond)
	for _, want := range []bool{true, false} {
		t0 := time.Now()
		toggle := `{"version":0,"type":"toggle","param":{"strip":{"kind":"muteGroup","index":2},"field":"mute"},"origin":{"kind":"gesture","clientId":"ui","seq":1}}`
		if err := s.eng.Apply(toggle); err != nil {
			t.Fatal(err)
		}
		m := s.mustReach(t, t0, 2*time.Second, func(m stateJSON) bool { return effectiveOf(m, 5) != nil }, "effectiveMute of ch 5")
		wire, _ := s.sim.Value("/config/mute/2")
		b, _ := json.Marshal(effectiveOf(m.st, 5))
		s.logf("toggle -> console /config/mute/2 = %d; diff after %d ms: ch 5 effectiveMute %s", wire.Int, m.at.Sub(t0).Milliseconds(), b)
		if (wire.Int == 1) != want {
			t.Errorf("toggle wrote %d", wire.Int)
		}
	}
}

func effectiveOf(m stateJSON, ch int) any {
	if m.Changes == nil {
		return nil
	}
	for _, c := range *m.Changes {
		if c.Param.Field == "effectiveMute" && c.Param.Strip.Index == ch {
			return c.Value
		}
	}
	return nil
}

// sceneRecallEndsHold: a desk recall of channels 1-16 ends a hold on ch 20.
func (s *session) sceneRecallEndsHold(t *testing.T) {
	s.logf("== scene recall at the desk while ch 20 is held")
	hold := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":20},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`
	if err := s.eng.Apply(hold); err != nil {
		t.Fatal(err)
	}
	scene := map[string]osc.Arg{}
	for ch := 1; ch <= 16; ch++ {
		scene[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.5)
	}
	t0 := time.Now()
	if err := s.sim.RecallScene(4, scene); err != nil {
		t.Fatal(err)
	}
	m := s.mustReach(t, t0, 2*time.Second, func(m stateJSON) bool { return hasEvent(m, "sceneRecalledAtConsole") }, "scene event")
	for _, ev := range m.st.Events {
		b, _ := json.Marshal(ev)
		s.logf("event after %d ms: %s", m.at.Sub(t0).Milliseconds(), b)
	}
	err := s.eng.Apply(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":20},"field":"fader"},"value":-5,"origin":{"kind":"gesture","clientId":"ui"}}`)
	s.logf("Apply to the held ch 20 after the recall -> %s", CodeOf(err))
	if CodeOf(err) != CodeGestureEnded || !hasEvent(m.st, "externalChangeOnHeld") {
		t.Errorf("the recall must end the held fader: %v", err)
	}
}

func hasEvent(m stateJSON, typ string) bool {
	for _, ev := range m.Events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// writesAudited: every write the console received was a user gesture.
func (s *session) writesAudited(t *testing.T) {
	time.Sleep(300 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	writes := s.sim.Writes()
	gestures := 0
	for _, a := range s.audit {
		if a.Origin.MayWrite() {
			gestures++
		}
	}
	s.logf("== console received %d writes; the engine audited %d, all with a gesture origin: %t", len(writes), len(s.audit), gestures == len(s.audit))
	if len(writes) != len(s.audit) || gestures != len(s.audit) {
		t.Errorf("writes %d, audit %d, gestures %d", len(writes), len(s.audit), gestures)
	}
}
