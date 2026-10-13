// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"context"
	"encoding/binary"
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

// TestSimSessionRedesign_Log shows core-api v0.5 (redesign CC-1 to CC-13)
// against sim/xair on real loopback UDP with the default timing, the
// simulator in its "no Gain echo" variant (X32-F79, XA-F116 unverified)
// and pushing /-snap/index (XA-F62). It writes a timestamped log when
// MIXCORE_REDESIGN_SESSION_LOG names the file:
//
//	MIXCORE_REDESIGN_SESSION_LOG=/tmp/redesign.log go test -tags=sim -run TestSimSessionRedesign_Log -v ./api/
func TestSimSessionRedesign_Log(t *testing.T) {
	path := os.Getenv("MIXCORE_REDESIGN_SESSION_LOG")
	if path == "" {
		t.Skip("set MIXCORE_REDESIGN_SESSION_LOG to write the session log")
	}
	f, err := os.Create(path) //nolint:gosec // G304: the developer names the log file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := newRedesignSession(t, io.MultiWriter(f, testWriter{t}))
	s.push(t)
	s.gainDrag(t)
	s.showModeHold(t)
	s.typed(t)
	s.undo(t)
	s.sceneAndWatch(t)
	s.pinAndFavourites(t)
	s.busMeters(t)
	s.audit(t)
}

type redesignSession struct{ *safetySession }

func newRedesignSession(t *testing.T, out io.Writer) *redesignSession {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simxair.New(simxair.Options{Name: "XR18-REDESIGN", NoGainPush: true, PushSnapIndex: true})
	s := &safetySession{out: out, start: time.Now(), sim: sim}
	_ = sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.6))      // Vox1 -6 dB (XA-F40)
	_ = sim.SurfaceSet("/headamp/03/gain", osc.Float(89.0/144)) // Gain 3 +32.5 dB
	_ = sim.SurfaceSet("/ch/04/mix/02/level", osc.Float(0.7))   // Keys into Mon 2: -2 dB
	_ = sim.SurfaceSet("/bus/2/mix/fader", osc.Float(0.375))    // Mon 2 -20 dB
	_ = sim.SurfaceSet("/bus/2/config/name", osc.String("Mon 2"))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	s.eng = NewEngine()
	s.eng.SetStateListener(s)
	t.Cleanup(func() { s.eng.Close(); cancel(); wg.Wait(); _ = pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	desc, err := s.eng.Start(fmt.Sprintf(`{"version":0,"family":"xair","host":"127.0.0.1","port":%d,"meters":{"inputs":true},"safety":{"slipGuard":true}}`, port))
	if err != nil {
		t.Fatal(err)
	}
	s.logf("sim/xair on 127.0.0.1:%d (NoGainPush, PushSnapIndex), real UDP, default timing; Start %s; v0.3 phone file slipGuard on", port, desc)
	for deadline := time.Now().Add(5 * time.Second); !s.has(`"status":"live"`); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("not live")
		}
	}
	s.logf("live; Vox1 -6.0 dB, Gain 3 +32.5 dB, Keys to Mon 2 -2.0 dB, Mon 2 master -20.0 dB")
	return &redesignSession{s}
}

// last returns the newest state message containing sub, or "".
func (s *redesignSession) last(sub string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.raw) - 1; i >= 0; i-- {
		if strings.Contains(s.raw[i], sub) {
			return s.raw[i]
		}
	}
	return ""
}

// jsonExcerpt cuts the JSON object starting at key out of msg (for the log).
func jsonExcerpt(msg, key string) string {
	i := strings.Index(msg, key)
	if i < 0 {
		return "(none)"
	}
	depth, j := 0, i+strings.IndexAny(key, "[{")
	for ; j < len(msg); j++ {
		switch msg[j] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		if depth == 0 && (msg[j] == ']' || msg[j] == '}') {
			return msg[i : j+1]
		}
	}
	return msg[i:]
}

func (s *redesignSession) push(t *testing.T) {
	s.logf("== CC-1: P2 on Vox1 (400 pt up in 150 ms); openWall -> %s", CodeOf(s.eng.Apply(gcmd("openWall", vox1, ""))))
	s.gesture(t, gcmd("holdBegin", vox1, ""))
	p0, stops := s.eng.FaderPosition(-6), 0
	for i := 1; i*8 <= 150; i++ {
		if r := s.gesture(t, gcmd("set", vox1, `,"value":`+levelText(s.eng.FaderDB(math.Min(p0+400*float64(i*8)/150/490, 1))))); r.State != "free" {
			stops++
		}
		time.Sleep(8 * time.Millisecond)
	}
	s.gesture(t, gcmd("holdEnd", vox1, ""))
	time.Sleep(100 * time.Millisecond)
	s.logf("released: mixer Vox1 %+.2f dB, %d stops before the top", s.mixerFader(1), stops)
}

func (s *redesignSession) gainDrag(t *testing.T) {
	s.logf("== CC-2, CC-3: Gain 3 drag, 20 steps at 40/s, no Gain echo")
	r := s.gesture(t, gcmd("holdBegin", gain3, ""))
	s.logf("holdBegin -> window %+.1f .. %+.1f dB (the headamp's range)", *r.Window.CapDownDB, *r.Window.CapUpDB)
	start, states := time.Now(), map[string]int{}
	for i := 1; i <= 20; i++ {
		r = s.gesture(t, gcmd("set", gain3, `,"value":`+levelText(32.5+float64(i))))
		states[r.State]++
		time.Sleep(25 * time.Millisecond)
	}
	s.gesture(t, gcmd("holdEnd", gain3, ""))
	time.Sleep(300 * time.Millisecond)
	reads, held := 0, s.has(`"type":"externalChangeOnHeld","param":{"strip":{"kind":"headamp"`)
	for _, e := range s.sim.Log() {
		if e.Class == simxair.ClassRead && e.Address == "/headamp/03/gain" && e.At.After(start) {
			reads++
			s.logf("settle re-read of /headamp/03/gain at +%d ms", e.At.Sub(start).Milliseconds())
		}
	}
	s.logf("states %v; mixer Gain %+.1f dB; externalChangeOnHeld for Gain: %t; last diff %s", states, s.mixerGain(), held,
		jsonExcerpt(s.last(`"index":3},"field":"gain"},"value"`), `{"param":{"strip":{"kind":"headamp","index":3}`))
	s.logf("outside a hold, +7 dB from the console in one write -> %s (SR-31)", CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":59.5`))))
	_ = s.eng.Apply(gcmd("set", gain3, `,"value":32.5,"typed":true`))
	time.Sleep(300 * time.Millisecond)
}

func (s *redesignSession) showModeHold(t *testing.T) {
	s.logf("== CC-4: show mode, Adjust (unlock), a finger resting 21 s on the wheel, then +1")
	_ = s.eng.Apply(gcmd("setShowMode", "", `,"value":true`))
	_ = s.eng.Apply(gcmd("unlock", gain3, ""))
	s.gesture(t, gcmd("holdBegin", gain3, ""))
	time.Sleep(21 * time.Second)
	r := s.gesture(t, gcmd("set", gain3, `,"value":33.5`))
	s.gesture(t, gcmd("holdEnd", gain3, ""))
	time.Sleep(300 * time.Millisecond)
	s.logf("after 21 s held: +1 -> state %s, written %t, gainUnlockEnded idle: %t", r.State, r.Written, s.has(`"reason":"idle"`))
	_ = s.eng.Apply(gcmd("setShowMode", "", `,"value":false`))
	_ = s.eng.Apply(gcmd("set", gain3, `,"value":32.5,"typed":true`))
	time.Sleep(300 * time.Millisecond)
}

func (s *redesignSession) typed(t *testing.T) {
	s.logf("== CC-5: typed raises over 6 dB on every level")
	for _, c := range []struct{ what, param, value string }{
		{"Mon 2 master -20 -> -10", `{"strip":{"kind":"bus","index":2},"field":"fader"}`, "-10"},
		{"Keys to Mon 2 -2 -> +6", send42, "6"},
		{"Gain 3 +32.5 -> +40", gain3, "40"},
	} {
		s.logf("typed %s -> %s", c.what, CodeOf(s.eng.Apply(gcmd("set", c.param, `,"value":`+c.value+`,"typed":true`))))
	}
}

func (s *redesignSession) undo(t *testing.T) {
	s.logf("== CC-6, CC-9: undo in the core")
	s.gesture(t, gcmd("holdBegin", vox1, ""))
	s.gesture(t, gcmd("set", vox1, `,"value":-5`))
	s.gesture(t, gcmd("set", vox1, `,"value":-14`))
	s.gesture(t, gcmd("holdEnd", vox1, ""))
	time.Sleep(200 * time.Millisecond)
	s.logf("feedback pull-down +10 -> -14 dB; state %s", jsonExcerpt(s.last(`"undo":[`), `"undo":[`))
	s.logf("undo at once -> %s (raises a level lowered < 30 s ago, more than 6 dB)", CodeOf(s.eng.Apply(gcmd("undo", "", ""))))
	err := s.eng.Apply(gcmd("undo", "", `,"confirmed":true`))
	time.Sleep(200 * time.Millisecond)
	s.logf("undo confirmed -> %q; mixer Vox1 %+.2f dB", CodeOf(err), s.mixerFader(1))
	s.gesture(t, gcmd("holdBegin", vox1, ""))
	s.gesture(t, gcmd("set", vox1, `,"value":0`))
	r := s.gesture(t, gcmd("cancelHold", vox1, ""))
	time.Sleep(200 * time.Millisecond)
	s.logf("held at 0 dB, Undo tapped (cancelHold) -> written %t, value %v; next set -> %s; mixer Vox1 %+.2f dB", r.Written, r.Value,
		CodeOf(s.eng.Apply(gcmd("set", vox1, `,"value":2`))), s.mixerFader(1))
	s.gesture(t, gcmd("holdEnd", vox1, ""))
	_ = s.eng.Apply(gcmd("toggle", vox1Mute, ""))
	time.Sleep(200 * time.Millisecond)
	s.logf("Vox1 muted; undo -> %s (unmute asks)", CodeOf(s.eng.Apply(gcmd("undo", "", ""))))
	_ = s.eng.Apply(gcmd("undo", "", `,"confirmed":true`))
	time.Sleep(200 * time.Millisecond)
}

func (s *redesignSession) sceneAndWatch(t *testing.T) {
	s.logf("== CC-11, CC-10: watch Mon 2, another client moves Keys into it; then the desk recalls snapshot 4")
	_ = s.eng.Apply(gcmd("watch", "", `,"scope":{"bus":2}`))
	_ = s.eng.Apply(strings.Replace(gcmd("set", send42, `,"value":-6`), `"clientId":"phone"`, `"clientId":"keys-iem"`, 1))
	time.Sleep(300 * time.Millisecond)
	s.logf("event %s", jsonExcerpt(s.last(`externalChangeInScope`), `{"type":"externalChangeInScope"`))
	s.gesture(t, gcmd("holdBegin", vox1, ""))
	s.gesture(t, gcmd("set", vox1, `,"value":-3`))
	s.gesture(t, gcmd("holdEnd", vox1, ""))
	scene := map[string]osc.Arg{}
	for ch := 1; ch <= 10; ch++ {
		scene[fmt.Sprintf("/ch/%02d/mix/fader", ch)] = osc.Float(0.5)
	}
	if err := s.sim.StoreSnapshot(4, "Song 4", scene); err != nil {
		t.Fatal(err)
	}
	_ = s.sim.LoadSnapshot(4)
	time.Sleep(2 * time.Second)
	s.logf("event %s", jsonExcerpt(s.last(`sceneRecalledAtConsole`), `{"type":"sceneRecalledAtConsole"`))
	s.logf("undo entry now %s", jsonExcerpt(s.last(`"beforeSceneRecall"`), `"undo":[`))
	s.logf("undo -> %s (across the recall)", CodeOf(s.eng.Apply(gcmd("undo", "", ""))))
	_ = s.eng.Apply(gcmd("watch", "", ""))
}

func (s *redesignSession) pinAndFavourites(t *testing.T) {
	s.logf("== CC-12, CC-7: show mode locked by a PIN; favourites")
	out, err := s.eng.Gesture(gcmd("lockShowMode", "", `,"pin":"2468"`))
	s.logf("lockShowMode -> %q, result has a pinRecord: %t", CodeOf(err), strings.Contains(out, `"pinRecord":"pbkdf2-sha256$`))
	s.logf("setShowMode false -> %s; unlock Gain -> %s; Gain -1 -> %q", CodeOf(s.eng.Apply(gcmd("setShowMode", "", `,"value":false`))),
		CodeOf(s.eng.Apply(gcmd("unlock", gain3, ""))), CodeOf(s.eng.Apply(gcmd("set", gain3, `,"value":31.5`))))
	s.logf("unlockShowMode wrong PIN -> %s; right PIN -> %q", CodeOf(s.eng.Apply(gcmd("unlockShowMode", "", `,"pin":"1111"`))),
		CodeOf(s.eng.Apply(gcmd("unlockShowMode", "", `,"pin":"2468"`))))
	_ = s.eng.Apply(gcmd("setShowMode", "", `,"value":false`))
	_ = s.eng.Apply(gcmd("setFavourites", "", `,"favourites":[{"kind":"input","index":1},{"kind":"bus","index":2}]`))
	time.Sleep(200 * time.Millisecond)
	s.logf("favourites in state: %s; the PIN record in any state message: %t", jsonExcerpt(s.last(`"favourites"`), `"favourites":[`), s.has("pbkdf2"))
}

func (s *redesignSession) busMeters(t *testing.T) {
	s.logf("== CC-13: bus meters in the frame, Main L/R last")
	s.sim.SetMeterValue(27, -1536) // bank 1 value 27 is bus 2 (XA-F53): -6 dBFS
	time.Sleep(time.Second)
	b := s.eng.MeterFrame()
	n := int(binary.LittleEndian.Uint16(b[6:]))
	lv := func(i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b[24+4*i:])) }
	snap := ""
	_ = s.eng.RequestSnapshot()
	time.Sleep(200 * time.Millisecond)
	snap = s.last(`"type":"snapshot"`)
	s.logf("frame n = %d; level 17 (bus 2) %v dBFS; Main L/R at %d/%d; snapshot bus 2: %s", n, lv(17), n-2, n-1,
		jsonExcerpt(snap, `{"index":2,"kind":"mix"`))
}

func (s *redesignSession) audit(t *testing.T) {
	snap := 0
	for _, e := range s.sim.Log() {
		if strings.HasPrefix(e.Address, "/-snap/") {
			snap++
		}
	}
	s.logf("== mixer received %d writes; packets to /-snap/: %d", len(s.sim.Writes()), snap)
	if snap != 0 {
		t.Errorf("%d packets to /-snap/", snap)
	}
}
