// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// Session-log sections for the review fixes and the headamp plan (see
// TestSimSessionSafety_Log).

const (
	keys     = `{"strip":{"kind":"input","index":3},"field":"fader"}`
	aux17    = `{"strip":{"kind":"headamp","index":17},"field":"gain"}`
	phantom1 = `{"strip":{"kind":"headamp","index":1},"field":"phantom"}`
	ptPerPos = 490.0 // portrait track length in pt (fixtures/gesture-paths.json)
)

// fullPush: redesign CC-1 (AC-RD-F01). Keys from -30 dB, dragged up 1 pt
// per 8 ms to the top of the 490 pt track: never stopped, never a jump.
func (s *safetySession) fullPush(t *testing.T) {
	s.logf("== a deliberate push is never stopped (CC-1, AC-RD-F01): Keys from -30 dB, 1 pt per 8 ms to the top")
	s.gesture(t, gcmd("holdBegin", keys, ""))
	p0, prev, maxStep, stops := s.eng.FaderPosition(-30), -30.0, 0.0, 0
	for finger := 1.0; p0+(finger-1)/ptPerPos < 1; finger++ {
		c := s.eng.FaderDB(math.Min(p0+finger/ptPerPos, 1))
		r := s.gesture(t, gcmd("set", keys, `,"value":`+levelText(c)))
		v, _ := r.Value.(float64)
		maxStep, prev = math.Max(maxStep, v-prev), v
		if r.State != "free" {
			stops++
		}
		time.Sleep(8 * time.Millisecond)
	}
	s.gesture(t, gcmd("holdEnd", keys, ""))
	time.Sleep(100 * time.Millisecond)
	s.logf("released: mixer Keys %+.2f dB, %d stops before the top; largest step between two applied values %.2f dB (one finger point is at most %.2f dB here)",
		s.mixerFader(3), stops, maxStep, 80/ptPerPos)
	if stops != 0 || s.mixerFader(3) < 9.99 {
		t.Errorf("the push stopped %d times or ended below +10 dB (%+.2f)", stops, s.mixerFader(3))
	}
}

// originsAndConfig: review MEDIUM 5 and LOW 6.
func (s *safetySession) originsAndConfig(t *testing.T) {
	s.logf("== typed entry only from a person (review MEDIUM 5), null ceiling refused (review LOW 6)")
	macro := func(extra string) string {
		return strings.Replace(gcmd("set", gain3, `,"value":45`+extra), `"gesture"`, `"macro"`, 1)
	}
	s.logf("a macro claims typed +45.0 dB -> %s", CodeOf(s.eng.Apply(macro(`,"typed":true`))))
	s.logf("a macro claims typed and confirmed +45.0 dB -> %s", CodeOf(s.eng.Apply(macro(`,"typed":true,"confirmed":true`))))
	other := NewEngine()
	_, err := other.Start(`{"version":0,"family":"xair","host":"127.0.0.1","safety":{"monitorCeiling":null}}`)
	other.Close()
	s.logf(`another engine starts with "monitorCeiling": null -> %s (refused while parsing, before any socket opens)`, CodeOf(err))
	if CodeOf(err) != CodeInvalidConfig {
		t.Errorf("null ceiling: %v", err)
	}
}

// headampsXAir: the per-headamp law on X Air (xair.md 3.6).
func (s *safetySession) headampsXAir(t *testing.T) {
	s.logf("== X Air headamps (XA-F110..F113): each with its own law")
	_ = s.eng.RequestSnapshot()
	time.Sleep(100 * time.Millisecond)
	for _, h := range s.snapshotHeadamps(t) {
		if h.Index == 1 || h.Index == 3 || h.Index == 17 {
			s.logf("snapshot headamp %2d: %s %d, gain %+.1f dB, law %+.0f..%+.0f step %.1f, writable %t, phantom %v",
				h.Index, h.Bank, h.Number, h.GainDB, h.MinDB, h.MaxDB, h.StepDB, h.Writable, phantomText(h.Phantom))
		}
	}
	s.logf("aux 17 Gain 0.0 (line law unconfirmed) -> %s", CodeOf(s.eng.Apply(gcmd("set", aux17, `,"value":0`))))
	s.logf("phantom on headamp 1 -> %s (read only; a future show-mode gate)", CodeOf(s.eng.Apply(gcmd("set", phantom1, `,"value":true`))))
	off, aux := 0, 0
	for _, w := range s.sim.Writes() {
		switch w.Address {
		case "/headamp/03/gain":
			if k := float64(w.Arg.Float) * 144; math.Abs(k-math.Round(k)) > 1e-3 {
				off++
			}
		case "/headamp/17/gain", "/headamp/01/phantom":
			aux++
		}
	}
	s.logf("headamp 3 Gain writes off the k/144 grid: %d; writes to aux 17 or phantom: %d", off, aux)
}

// snapshotHeadamps returns the headamps of the latest snapshot.
func (s *safetySession) snapshotHeadamps(t *testing.T) []headampJSON {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.raw) - 1; i >= 0; i-- {
		var m stateJSON
		if json.Unmarshal([]byte(s.raw[i]), &m) == nil && m.Type == "snapshot" && len(m.Headamps) > 0 {
			return m.Headamps
		}
	}
	t.Fatal("no snapshot with headamps")
	return nil
}

func phantomText(p *bool) string {
	if p == nil {
		return "not read"
	}
	return fmt.Sprint(*p)
}

// logX32Headamps: X32 Gain, phantom and the channel patch (X32-F70..F76)
// against sim/x32 on loopback, default timing.
func (s *safetySession) logX32Headamps(t *testing.T) {
	s.logf("== X32 headamps (X32-F70..F76) against sim/x32, real UDP, default timing")
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{Name: "X32-SAFE"})
	_ = sim.SurfaceSet("/headamp/040/gain", osc.Float(44.0/144)) // AES50 A 09 at +10 dB
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	x := &safetySession{out: s.out, start: s.start, eng: NewEngine()}
	x.eng.SetStateListener(x)
	defer func() { x.eng.Close(); cancel(); wg.Wait(); _ = pc.Close() }()
	began := time.Now()
	if _, err := x.eng.Start(fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d}`, pc.LocalAddr().(*net.UDPAddr).Port)); err != nil {
		t.Fatal(err)
	}
	for !x.has(`"status":"live"`) {
		if time.Since(began) > 5*time.Second {
			t.Fatal("x32 not live")
		}
		time.Sleep(time.Millisecond)
	}
	s.logf("x32 live %d ms after Start; reads at connect: %d, of AES50 headamps: %d", time.Since(began).Milliseconds(), len(sim.Log()), len(aes50ReadsOf(sim)))
	hs := x.snapshotHeadamps(t)
	s.logf("snapshot: %d headamps; headamp 1 = %s %d, law %+.0f..%+.0f step %.1f, writable %t, phantom %s; ch 5 headamp %s",
		len(hs), hs[0].Bank, hs[0].Number, hs[0].MinDB, hs[0].MaxDB, hs[0].StepDB, hs[0].Writable, phantomText(hs[0].Phantom), x.inputHeadamp(5))
	_ = sim.SurfaceSet("/-ha/07/index", osc.Int(40)) // ch 8 patched to AES50 A 09 at the desk
	time.Sleep(200 * time.Millisecond)
	for _, h := range x.snapshotHeadamps(t) {
		if h.Index == 41 {
			s.logf("patch change ch 8 -> /headamp/040: ch 8 headamp %s; new snapshot entry %d = %s %d, gain %+.1f dB, writable %t; AES50 reads so far %v",
				x.inputHeadamp(8), h.Index, h.Bank, h.Number, h.GainDB, h.Writable, aes50ReadsOf(sim))
		}
	}
	aes := `{"strip":{"kind":"headamp","index":41},"field":"gain"}`
	s.logf("Gain on AES50 A 09 -> %s (read-only until hardware item 21)", CodeOf(x.eng.Apply(gcmd("set", aes, `,"value":12`))))
	local := `{"strip":{"kind":"headamp","index":1},"field":"gain"}`
	err = x.eng.Apply(gcmd("set", local, `,"value":0.5`))
	time.Sleep(100 * time.Millisecond)
	v, _ := sim.Value("/headamp/000/gain")
	s.logf("Gain on Local 1 to +0.5 dB -> %q; wire float32 %08x (x32.md table: 3e31c71c); console writes in total %d",
		CodeOf(err), math.Float32bits(v.Float), len(sim.Writes()))
}

// inputHeadamp renders an input's headamp field from the latest snapshot.
func (s *safetySession) inputHeadamp(ch int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.raw) - 1; i >= 0; i-- {
		var m stateJSON
		if json.Unmarshal([]byte(s.raw[i]), &m) != nil || m.Type != "snapshot" {
			continue
		}
		for _, st := range m.Strips {
			if st.Kind == "input" && st.Index == ch {
				return string(st.Headamp)
			}
		}
	}
	return "absent"
}

func aes50ReadsOf(sim *simx32.Sim) []string {
	var out []string
	for _, e := range sim.Log() {
		var n int
		if e.Class == simx32.ClassRead && strings.HasPrefix(e.Address, "/headamp/") {
			if _, err := fmt.Sscanf(e.Address, "/headamp/%03d/", &n); err == nil && n >= 32 {
				out = append(out, e.Address)
			}
		}
	}
	return out
}
