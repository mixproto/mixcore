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
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// TestSimSessionSafety_Log shows the phone safety rules of core-api v0.3
// to v0.5 against sim/xair on real loopback UDP with the default timing:
// a deliberate push never stopped although the v0.3 phone setting
// slipGuard is on (v0.5, CC-1), a slow push to the top, a pull down never
// stopped, the Gain cap and
// per-write bound (also against the value in flight), show mode refusals
// with an unlock and the undo grace of its entries, the typed-raise
// confirm and its gesture-only origin, the per-headamp Gain law on X Air
// and, against sim/x32, the X32 headamps and a patch change. It writes a
// timestamped log when MIXCORE_SAFETY_SESSION_LOG names the file:
//
//	MIXCORE_SAFETY_SESSION_LOG=/tmp/safety.log go test -tags=sim -run TestSimSessionSafety_Log -v ./api/
func TestSimSessionSafety_Log(t *testing.T) {
	path := os.Getenv("MIXCORE_SAFETY_SESSION_LOG")
	if path == "" {
		t.Skip("set MIXCORE_SAFETY_SESSION_LOG to write the session log")
	}
	f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := newSafetySession(t, io.MultiWriter(f, testWriter{t}))
	s.slip(t)
	s.fullPush(t)
	s.pullDown(t)
	s.gain(t)
	s.showMode(t)
	s.typedRaise(t)
	s.originsAndConfig(t)
	s.headampsXAir(t)
	s.audit(t)
	s.logX32Headamps(t)
}

type safetySession struct {
	out   io.Writer
	start time.Time
	sim   *simxair.Sim
	eng   *Engine
	mu    sync.Mutex
	raw   []string
}

func (s *safetySession) OnState(js string) { s.mu.Lock(); s.raw = append(s.raw, js); s.mu.Unlock() }

func (s *safetySession) logf(format string, args ...any) {
	fmt.Fprintf(s.out, "[%8.3f s] %s\n", time.Since(s.start).Seconds(), fmt.Sprintf(format, args...))
}

func newSafetySession(t *testing.T, out io.Writer) *safetySession {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &safetySession{out: out, start: time.Now(), sim: simxair.New(simxair.Options{Name: "XR18-SAFE"})}
	_ = s.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.6))      // Vox1 -6 dB (XA-F40)
	_ = s.sim.SurfaceSet("/ch/02/mix/fader", osc.Float(0.375))    // Vox2 -20 dB
	_ = s.sim.SurfaceSet("/ch/03/mix/fader", osc.Float(0.25))     // Keys -30 dB
	_ = s.sim.SurfaceSet("/headamp/03/gain", osc.Float(89.0/144)) // Gain +32.5 dB
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = s.sim.Serve(ctx, pc) })
	s.eng = NewEngine()
	s.eng.SetStateListener(s)
	t.Cleanup(func() { s.eng.Close(); cancel(); wg.Wait(); _ = pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	cfg := fmt.Sprintf(`{"version":0,"family":"xair","host":"127.0.0.1","port":%d,"safety":{"slipGuard":true}}`, port)
	desc, err := s.eng.Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.logf("sim/xair on 127.0.0.1:%d, real UDP, default timing; Start %s; config safety.slipGuard on (a v0.3 phone file; ignored since v0.5)", port, desc)
	deadline := time.Now().Add(5 * time.Second)
	for !s.has(`"status":"live"`) {
		if time.Now().After(deadline) {
			t.Fatal("not live")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.logf("live; fixture Vox1 -6.0 dB, Vox2 -20.0 dB, Keys -30.0 dB, headamp 3 Gain +32.5 dB")
	return s
}

func (s *safetySession) has(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(strings.Join(s.raw, "\n"), sub)
}

func gcmd(typ, param, extra string) string {
	p := ""
	if param != "" {
		p = `,"param":` + param
	}
	return `{"version":0,"type":"` + typ + `"` + p + extra + `,"origin":{"kind":"gesture","clientId":"phone","seq":1}}`
}

func (s *safetySession) gesture(t *testing.T, cmd string) gestureResultJSON {
	t.Helper()
	out, err := s.eng.Gesture(cmd)
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	var r gestureResultJSON
	_ = json.Unmarshal([]byte(out), &r)
	return r
}

func (s *safetySession) mixerFader(ch int) float64 {
	v, _ := s.sim.Value(fmt.Sprintf("/ch/%02d/mix/fader", ch))
	return s.eng.FaderDB(float64(v.Float))
}

func (s *safetySession) mixerGain() float64 {
	v, _ := s.sim.Value("/headamp/03/gain")
	return -12 + 72*float64(v.Float)
}

// slip: P2 portrait on Vox1 (up 400 pt in 150 ms, 8 ms samples, track
// 490 pt): v0.5 (CC-1, AC-RD-F01) the value follows the finger to the top.
func (s *safetySession) slip(t *testing.T) {
	s.logf("== a deliberate push (CC-1, AC-RD-F01): P2 on Vox1, finger up 400 pt in 150 ms")
	vox1 := `{"strip":{"kind":"input","index":1},"field":"fader"}`
	r := s.gesture(t, gcmd("holdBegin", vox1, ""))
	s.logf("holdBegin -> start %.2f dB, opensByRest %t (deprecated, always false)", r.Window.StartDB, r.Window.OpensByRest)
	s.logf("openWall -> %s (retired, CC-1)", CodeOf(s.eng.Apply(gcmd("openWall", vox1, ""))))
	p0, stops := s.eng.FaderPosition(-6), 0
	for i := 1; i*8 <= 150; i++ {
		c := s.eng.FaderDB(math.Min(p0+400*float64(i*8)/150/490, 1))
		r = s.gesture(t, gcmd("set", vox1, `,"value":`+levelText(c)))
		if r.State != "free" {
			stops++
		}
		time.Sleep(8 * time.Millisecond)
	}
	s.gesture(t, gcmd("holdEnd", vox1, ""))
	time.Sleep(100 * time.Millisecond)
	s.logf("finger at the top: last applied %+.2f dB, %d stops; mixer Vox1 %+.2f dB", r.Value, stops, s.mixerFader(1))
	if stops != 0 || s.mixerFader(1) < 9.99 {
		t.Errorf("P2 stopped %d times or ended at %+.2f dB", stops, s.mixerFader(1))
	}
}

// pullDown: P1D on Vox2 (down 60 pt in 40 ms): never stopped.
func (s *safetySession) pullDown(t *testing.T) {
	s.logf("== pull down (downward is never stopped): P1D on Vox2 from -20 dB")
	vox2 := `{"strip":{"kind":"input","index":2},"field":"fader"}`
	s.gesture(t, gcmd("holdBegin", vox2, ""))
	p0 := s.eng.FaderPosition(-20)
	var r gestureResultJSON
	for i := 1; i <= 5; i++ {
		c := s.eng.FaderDB(p0 - 60*float64(i)/5/490)
		r = s.gesture(t, gcmd("set", vox2, `,"value":`+levelText(c)))
	}
	s.gesture(t, gcmd("holdEnd", vox2, ""))
	time.Sleep(100 * time.Millisecond)
	s.logf("finger 60 pt down in 40 ms -> applied %+.2f dB, state %s; mixer Vox2 %+.2f dB (no stop)", r.Value, r.State, s.mixerFader(2))
}

// gain: a drag to +60 is capped at +3 dB; a bound refusal; typed confirm.
func (s *safetySession) gain(t *testing.T) {
	s.logf("== Gain caps and bound (SR-24, SR-31, CR-23) on headamp 3 at +32.5 dB")
	r := s.gesture(t, gcmd("holdBegin", gain3, ""))
	s.logf("holdBegin -> caps %+.1f / %+.1f dB", *r.Window.CapUpDB, *r.Window.CapDownDB)
	r = s.gesture(t, gcmd("set", gain3, `,"value":60`))
	s.logf("drag candidate +60.0 dB -> applied %v dB, state %s ('Lift and drag again')", r.Value, r.State)
	s.gesture(t, gcmd("holdEnd", gain3, ""))
	time.Sleep(100 * time.Millisecond)
	s.logf("mixer Gain %+.1f dB", s.mixerGain())
	for _, c := range []struct{ what, cmd string }{
		{"a macro sets +45.0 dB", strings.Replace(gcmd("set", gain3, `,"value":45`), `"gesture"`, `"macro"`, 1)},
		{"an undo sets +45.0 dB", gcmd("set", gain3, `,"value":45,"undo":true`)},
		{"typed +45.0 dB", gcmd("set", gain3, `,"value":45,"typed":true`)},
	} {
		s.logf("%s -> %s", c.what, CodeOf(s.eng.Apply(c.cmd)))
	}
	err := s.eng.Apply(gcmd("set", gain3, `,"value":45,"typed":true,"confirmed":true`))
	time.Sleep(100 * time.Millisecond)
	s.logf("typed +45.0 dB, confirmed -> %q; mixer Gain %+.1f dB", CodeOf(err), s.mixerGain())
	_ = s.eng.Apply(gcmd("set", gain3, `,"value":32.5,"typed":true`)) // back to the fixture
	time.Sleep(100 * time.Millisecond)                                // its echo
	_ = s.eng.Apply(strings.Replace(gcmd("set", gain3, `,"value":26.5`), `"gesture"`, `"macro"`, 1))
	err = s.eng.Apply(strings.Replace(gcmd("set", gain3, `,"value":38.5`), `"gesture"`, `"macro"`, 1))
	time.Sleep(100 * time.Millisecond)
	s.logf("a macro sets +26.5 then at once +38.5 (+6 from the console, +12 from the value in flight; review HIGH 1) -> %s; mixer Gain %+.1f dB",
		CodeOf(err), s.mixerGain())
	_ = s.eng.Apply(gcmd("set", gain3, `,"value":32.5,"typed":true`))
	time.Sleep(100 * time.Millisecond)
}

// showMode: refusals, lowering, unlock and relock, undo grace.
func (s *safetySession) showMode(t *testing.T) {
	s.logf("== show mode (SR-27, CR-24)")
	_ = s.eng.Apply(gcmd("setShowMode", "", `,"value":true`))
	s.logf("setShowMode true -> writes so far %d (turning it on writes nothing)", len(s.sim.Writes()))
	s.logf("Gain +1 (33.5) -> %s", CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":33.5`))))
	s.logf("Gain typed 40.0 confirmed -> %s", CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":40,"typed":true,"confirmed":true`))))
	s.gesture(t, gcmd("holdBegin", gain3, ""))
	r := s.gesture(t, gcmd("set", gain3, `,"value":34`))
	s.logf("Gain drag up to 34.0 -> applied %v dB, state %s ('Raising Gain is locked in show mode')", r.Value, r.State)
	s.gesture(t, gcmd("holdEnd", gain3, ""))
	s.logf("Gain -1 (31.5) -> %q (lowering stays live)", CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":31.5`))))
	time.Sleep(100 * time.Millisecond)
	s.logf("Vox1 fader -10 dB -> %q (faders stay live)", CodeOf(s.eng.Apply(gcmd("set", `{"strip":{"kind":"input","index":1},"field":"fader"}`, `,"value":-10`))))
	_ = s.eng.Apply(gcmd("set", gain3, `,"value":30.5`))
	s.logf("Gain lowered to 30.5, then at once +1 (31.5) -> %s (a raise above the value in flight; review HIGH 1)",
		CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":31.5`))))
	time.Sleep(100 * time.Millisecond)
	_ = s.eng.Apply(gcmd("unlock", gain3, ""))
	s.logf("unlock (after the UI's one-tap Adjust, v0.5); Gain +1 (31.5) -> %q", CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":31.5`))))
	time.Sleep(100 * time.Millisecond)
	s.logf("while unlocked, Gain -2 (29.5) -> %q", CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":29.5`))))
	time.Sleep(100 * time.Millisecond)
	_ = s.eng.Apply(gcmd("relock", gain3, ""))
	time.Sleep(100 * time.Millisecond)
	s.logf("relock (sheet closed): event in state %t; Gain +1 (30.5) -> %s", s.has(`"type":"gainUnlockEnded","reason":"relock","index":3`),
		CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":30.5`))))
	s.logf("undo raising to 36.5 within 10 s, never a value of that unlock (review LOW 7) -> %s",
		CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":36.5,"undo":true`))))
	err := s.eng.Apply(gcmd("set", gain3, `,"value":31.5,"undo":true`))
	time.Sleep(100 * time.Millisecond)
	s.logf("undo of the last unlock entry within 10 s (29.5 back to 31.5) -> %q; mixer Gain %+.1f dB", CodeOf(err), s.mixerGain())
	_ = s.eng.Apply(gcmd("setShowMode", "", `,"value":false`))
	_ = s.eng.Apply(gcmd("set", gain3, `,"value":32.5,"typed":true`))
	time.Sleep(100 * time.Millisecond)
}

// typedRaise: AC-PS-16 on Vox2.
func (s *safetySession) typedRaise(t *testing.T) {
	vox2 := `{"strip":{"kind":"input","index":2},"field":"fader"}`
	_ = s.eng.Apply(gcmd("set", vox2, `,"value":-20,"typed":true,"confirmed":true`)) // back to the fixture
	time.Sleep(100 * time.Millisecond)
	s.logf("== typed raises (SR-30, CR-26) on Vox2 at %.1f dB", s.mixerFader(2))
	s.logf("typed 0.0 from -20.0 -> %s (zero writes; UI: 'Raise Vox2 fader from -20.0 to 0.0 dB?')", CodeOf(s.eng.Apply(gcmd("set", vox2, `,"value":0,"typed":true`))))
	err := s.eng.Apply(gcmd("set", vox2, `,"value":0,"typed":true,"confirmed":true`))
	time.Sleep(100 * time.Millisecond)
	s.logf("typed 0.0 confirmed -> %q; mixer Vox2 %+.2f dB", CodeOf(err), s.mixerFader(2))
	s.logf("typed -2.0 from 0.0 -> %q (lowering never asks)", CodeOf(s.eng.Apply(gcmd("set", vox2, `,"value":-2,"typed":true`))))
}

func (s *safetySession) audit(t *testing.T) {
	time.Sleep(200 * time.Millisecond)
	snap := 0
	for _, e := range s.sim.Log() {
		if strings.HasPrefix(e.Address, "/-snap/") {
			snap++
		}
	}
	maxV1 := math.Inf(-1)
	for _, w := range s.sim.Writes() {
		if w.Address == "/ch/01/mix/fader" {
			maxV1 = math.Max(maxV1, s.eng.FaderDB(float64(w.Arg.Float)))
		}
	}
	s.logf("== mixer received %d writes; highest Vox1 write %+.2f dB (the deliberate push); packets to /-snap/: %d", len(s.sim.Writes()), maxV1, snap)
	if snap != 0 {
		t.Errorf("%d packets to /-snap/", snap)
	}
}
