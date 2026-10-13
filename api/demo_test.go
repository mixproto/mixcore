// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/memnet"
)

const echoWait = 500 * time.Millisecond

func TestParseDemoRequest(t *testing.T) {
	for _, bad := range []string{``, `{}`, `{"version":1,"family":"xair"}`, `{"version":0,"family":"wing"}`, `{"version":0,"family":"dlive"}`,
		`{"version":0,"family":"xair","host":"10.0.0.1"}`, `{"version":0,"family":"xair","deskChanges":"yes"}`} {
		if _, err := parseDemoRequest(bad); CodeOf(err) != CodeInvalidConfig {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	r, err := parseDemoRequest(`{"version":0,"family":"x32","deskChanges":true}`)
	if err != nil || r.Family != "x32" || !r.DeskChanges {
		t.Fatalf("%+v %v", r, err)
	}
}

func FuzzParseDemoRequest(f *testing.F) {
	f.Add(`{"version":0,"family":"xair"}`)
	f.Add(`{"version":0,"family":"x32","deskChanges":true}`)
	f.Fuzz(func(t *testing.T, in string) {
		r, err := parseDemoRequest(in)
		if err != nil {
			if CodeOf(err) != CodeInvalidConfig {
				t.Fatalf("code %s", CodeOf(err))
			}
			return
		}
		if !knownFamily(r.Family) {
			t.Fatalf("accepted family %q", r.Family)
		}
	})
}

// demoEnv is an engine whose demo and sessions run on an in-memory network
// (call inside synctest).
type demoEnv struct {
	net   *memnet.Network
	eng   *Engine
	rec   *recorder
	audit *[]driver.WriteRecord
}

func newDemoEnv(t *testing.T) *demoEnv {
	t.Helper()
	n := memnet.New()
	audit := &[]driver.WriteRecord{}
	eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2"), DemoListenPacket: n.Host("127.0.0.1"),
		WriteAudit: func(w driver.WriteRecord) { *audit = append(*audit, w) }})
	rec := &recorder{}
	eng.SetStateListener(rec)
	t.Cleanup(eng.Close)
	return &demoEnv{net: n, eng: eng, rec: rec, audit: audit}
}

func (d *demoEnv) startDemo(t *testing.T, family string) demoMixerJSON {
	t.Helper()
	out, err := d.eng.StartDemo(`{"version":0,"family":"` + family + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	var m demoMixerJSON
	if err := decodeStrict(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func demoConfig(m demoMixerJSON) string {
	return fmt.Sprintf(`{"version":0,"family":%q,"host":%q,"port":%d,"meters":{"inputs":true},"safety":{"slipGuard":true}}`,
		m.Family, m.Host, m.Port)
}

// The journey of the demo button: StartDemo, Start, a gesture and its echo,
// meters with Main L/R, StopDemo; every message says demo, and the only
// write the console sees is the user's.
func TestDemo_Journey(t *testing.T) {
	for _, fam := range []string{familyX32, familyXAir} {
		t.Run(fam, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { demoJourney(t, fam) })
		})
	}
}

func demoJourney(t *testing.T, fam string) {
	d := newDemoEnv(t)
	m := d.startDemo(t, fam)
	if m.Host != "127.0.0.1" || m.Port == 0 || m.Family != fam || m.Name != "Demo" {
		t.Fatalf("demo mixer %+v", m)
	}
	desc, err := d.eng.Start(demoConfig(m))
	if err != nil || !strings.Contains(desc, `"demo":true`) {
		t.Fatalf("session %s, %v", desc, err)
	}
	time.Sleep(time.Second)
	synctest.Wait()
	st := d.rec.last()
	if st.Status != "live" || !st.Demo || st.Strips[0].Name != "Kick" || st.Strips[12].EffectiveMute == nil ||
		!mutedTrue(st.Strips[12].EffectiveMute) {
		t.Fatalf("live snapshot %+v", st)
	}
	assertDemoHeadamps(t, st, fam)
	if err := d.eng.Apply(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},` +
		`"value":-10,"origin":{"kind":"gesture","clientId":"ui","seq":1}}`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(echoWait)
	synctest.Wait()
	echo := findChange(d.rec.all(), "input", 1, "fader")
	if echo == nil || echo.Origin.ClientID != "ui" || echo.Origin.Seq != 1 || math.Abs(echo.Value.(float64)+10) > 0.1 {
		t.Fatalf("echo %+v", echo)
	}
	assertDemoMeters(t, d.eng, fam)
	assertDemoSafetyPath(t, d)
	for _, raw := range d.rec.raw {
		if !strings.Contains(raw, `"demo":true`) {
			t.Fatalf("a demo session message without demo: %s", raw)
		}
	}
	if len(*d.audit) != 3 {
		t.Fatalf("%d writes reached the demo console, want the three gestures", len(*d.audit))
	}
	if err := d.eng.StopDemo(); err != nil {
		t.Fatal(err)
	}
	if st := d.rec.last(); st.Status != "stopped" {
		t.Fatalf("after StopDemo the session is %q", st.Status)
	}
	if err := d.eng.Apply(cmd("fader", "-5", "gesture")); CodeOf(err) != CodeNotStarted {
		t.Fatalf("Apply after StopDemo: %v", err)
	}
}

func findChange(msgs []stateJSON, kind string, index int, field string) *changeJSON {
	for _, m := range msgs {
		if c := changeIn(m, kind, index, field); c != nil {
			return c
		}
	}
	return nil
}

func changeIn(m stateJSON, kind string, index int, field string) *changeJSON {
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

// assertDemoMeters reads two frames 300 ms apart: the family's channel map,
// moving inputs, and a sounding Main L/R.
func assertDemoMeters(t *testing.T, eng *Engine, fam string) {
	t.Helper()
	want := map[string]int{familyX32: 50, familyXAir: 28}[fam] // v0.5: bus meters between the inputs and Main L/R
	a := eng.MeterFrame()
	time.Sleep(300 * time.Millisecond)
	synctest.Wait()
	b := eng.MeterFrame()
	n := int(binary.LittleEndian.Uint16(b[6:]))
	lv := func(f []byte, i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(f[24+4*i:])) }
	if n != want || binary.LittleEndian.Uint64(b[8:]) <= binary.LittleEndian.Uint64(a[8:]) {
		t.Fatalf("meter frame n = %d (want %d), seq %d -> %d", n, want,
			binary.LittleEndian.Uint64(a[8:]), binary.LittleEndian.Uint64(b[8:]))
	}
	moved := false
	for i := range n - 2 {
		moved = moved || lv(a, i) != lv(b, i)
	}
	busFirst := map[string]int{familyX32: 32, familyXAir: 16}[fam]
	for bus := range 6 { // the six monitor mixes carry the band: their meters move (v0.5, CC-13)
		if l := lv(b, busFirst+bus); l < -60 || l > 0 {
			t.Fatalf("%s bus %d meter %v dBFS", fam, bus+1, l)
		}
	}
	mainL, mainR := lv(b, n-2), lv(b, n-1)
	if !moved || mainL < -40 || mainL > 0 || mainR < -40 || mainR > 0 {
		t.Fatalf("inputs moved %v; Main L/R %v / %v dBFS", moved, mainL, mainR)
	}
}

func TestDemo_StartStopCodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDemoEnv(t)
		if err := d.eng.StopDemo(); CodeOf(err) != CodeNotStarted {
			t.Fatalf("StopDemo without a demo: %v", err)
		}
		m := d.startDemo(t, familyXAir)
		if _, err := d.eng.StartDemo(`{"version":0,"family":"x32"}`); CodeOf(err) != CodeAlreadyStarted {
			t.Fatalf("second StartDemo: %v", err)
		}
		if _, err := d.eng.StartDemo(`{"version":0}`); CodeOf(err) != CodeInvalidConfig {
			t.Fatalf("bad request: %v", err)
		}
		wrong := fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d}`, m.Port)
		if _, err := d.eng.Start(wrong); CodeOf(err) != CodeInvalidConfig {
			t.Fatalf("Start to the xair demo as x32: %v", err)
		}
		if err := d.eng.StopDemo(); err != nil {
			t.Fatal(err)
		}
		// The same port is free again, and no longer the demo: a session there is a real console.
		desc, err := d.eng.Start(demoConfig(m))
		if err != nil || strings.Contains(desc, "demo") {
			t.Fatalf("after StopDemo: %s %v", desc, err)
		}
		if err := d.eng.StopDemo(); CodeOf(err) != CodeNotStarted {
			t.Fatalf("StopDemo after StopDemo: %v", err)
		}
		if err := d.eng.Stop(); err != nil {
			t.Fatalf("a non-demo session must survive StopDemo: %v", err)
		}
	})
}

// A real console's session never says demo, even while a demo runs.
func TestDemo_RealSessionIsNotDemo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDemoEnv(t)
		d.startDemo(t, familyX32)
		desc, err := d.eng.Start(`{"version":0,"family":"x32","host":"10.0.0.1"}`)
		if err != nil || strings.Contains(desc, "demo") {
			t.Fatalf("%s %v", desc, err)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		for _, raw := range d.rec.raw {
			if strings.Contains(raw, `"demo"`) {
				t.Fatalf("real session message says demo: %s", raw)
			}
		}
		if err := d.eng.StopDemo(); err != nil {
			t.Fatal(err)
		}
		if err := d.eng.Stop(); err != nil {
			t.Fatalf("StopDemo stopped a real console's session: %v", err)
		}
	})
}

func TestDemo_WithoutDemoFiltersOnlyTheDemo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDemoEnv(t)
		found := func(host string, port int) x32.Found {
			return x32.Found{Endpoint: driver.Endpoint{Host: host, Port: port}}
		}
		all := []x32.Found{found("127.0.0.1", 10024), found("192.168.1.1", 10024)}
		if got := d.eng.demo.withoutDemo(all); len(got) != 2 {
			t.Fatalf("no demo running: %v", got)
		}
		m := d.startDemo(t, familyXAir)
		all = []x32.Found{found("127.0.0.1", m.Port), found("::ffff:127.0.0.1", m.Port), found("192.168.1.1", 10024)}
		got := d.eng.demo.withoutDemo(all)
		if len(got) != 1 || got[0].Endpoint.Host != "192.168.1.1" {
			t.Fatalf("demo listed: %v", got)
		}
	})
}

func TestDemo_SessionJSONSchemaShape(t *testing.T) {
	desc, err := describe(configJSON{Family: familyXAir, Host: "127.0.0.1", Port: 5}, defaultDrivers()[familyXAir], true)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(desc), &m); err != nil || m["demo"] != true {
		t.Fatalf("%s %v", desc, err)
	}
}

// With deskChanges, a fader moves at the "desk" every 30 s. If the user holds
// it, the core ends the gesture with the console's value (README safety
// rule 4), exactly as with a real console.
func TestDemo_DeskChangeEndsAHeldFader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newDemoEnv(t)
		out, err := d.eng.StartDemo(`{"version":0,"family":"xair","deskChanges":true}`)
		if err != nil {
			t.Fatal(err)
		}
		var m demoMixerJSON
		if err := decodeStrict(out, &m); err != nil {
			t.Fatal(err)
		}
		if _, err := d.eng.Start(demoConfig(m)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		hold := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":10},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`
		if err := d.eng.Apply(hold); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		var ended *eventJSON
		for _, st := range d.rec.all() {
			for i, ev := range st.Events {
				if ev.Type == "externalChangeOnHeld" && ev.Param != nil && ev.Param.Strip.Index == 10 {
					ended = &st.Events[i]
				}
			}
		}
		if ended == nil || ended.Origin == nil || ended.Origin.Kind != "console" {
			t.Fatalf("the desk move must end the held Vox 1 fader; event %+v", ended)
		}
		if db := ended.Value.(float64); math.Abs(db-(-2+3)) > 0.1 {
			t.Fatalf("console value %v dB, want the desk's -2 + 3 dB", db)
		}
		if len(*d.audit) != 0 {
			t.Fatalf("a desk change must not be a write of ours: %v", *d.audit)
		}
	})
}

// The examples printed in the demo contracts are what the code accepts and returns.
func TestDemoContractExamples(t *testing.T) {
	for file, parse := range map[string]func(string) error{
		"../contracts/core-api/demo.schema.json": func(s string) error { _, err := parseDemoRequest(s); return err },
		"../contracts/core-api/demo-mixer.schema.json": func(s string) error {
			var m demoMixerJSON
			return decodeStrict(s, &m)
		},
	} {
		b, err := os.ReadFile(file) //nolint:gosec // G304: the fixed contract paths above
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Examples []json.RawMessage `json:"examples"`
		}
		if err := json.Unmarshal(b, &schema); err != nil || len(schema.Examples) == 0 {
			t.Fatalf("%s: %v, %d examples", file, err, len(schema.Examples))
		}
		for _, ex := range schema.Examples {
			if err := parse(string(ex)); err != nil {
				t.Fatalf("%s example %s rejected: %v", file, ex, err)
			}
		}
	}
}

// Nothing of the demo exists until StartDemo, and nothing is left after
// StopDemo: no simulator or animator goroutine, no socket.
func TestDemo_NothingRunsUnlessStarted(t *testing.T) {
	demoRunning := func() bool {
		buf := make([]byte, 1<<20)
		stacks := buf[:runtime.Stack(buf, true)]
		return bytes.Contains(stacks, []byte("mixcore/demo.")) || bytes.Contains(stacks, []byte("sim/internal/oscsim."))
	}
	eng := NewEngineWith(Options{DemoListenPacket: memnet.New().Host("127.0.0.1")}) // unit tests open no OS socket
	defer eng.Close()
	if demoRunning() {
		t.Fatal("a demo goroutine runs before StartDemo")
	}
	if _, err := eng.StartDemo(`{"version":0,"family":"xair"}`); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); !demoRunning(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) { // a new goroutine shows its frames once it has run
			t.Fatal("the probe must see a running demo")
		}
	}
	if err := eng.StopDemo(); err != nil {
		t.Fatal(err)
	}
	if demoRunning() {
		t.Fatal("a demo goroutine survived StopDemo")
	}
}

// A demo that answers a probe and is stopped before the probe ends is still
// never listed: the filter knows every demo this engine has run.
func TestDemo_DiscoveryNeverListsADemoStoppedDuringTheProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		eng := NewEngineWith(Options{ListenPacket: n.Host("127.0.0.1"), DemoListenPacket: n.Host("127.0.0.1")})
		defer eng.Close()
		rec := &discoveryRecorder{}
		eng.SetDiscoveryListener(rec)
		out, err := eng.StartDemo(`{"version":0,"family":"xair"}`)
		if err != nil {
			t.Fatal(err)
		}
		var m demoMixerJSON
		_ = decodeStrict(out, &m)
		req := fmt.Sprintf(`{"version":0,"family":"xair","hosts":[{"host":"127.0.0.1","port":%d}],"waitMs":500}`, m.Port)
		if err := eng.Discover(req); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if err := eng.StopDemo(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if len(rec.msgs) != 1 || rec.msgs[0].Status != "done" || len(rec.msgs[0].Consoles) != 0 {
			t.Fatalf("discovery listed the demo: %+v", rec.msgs)
		}
	})
}

func TestDemoSlot_RemembersABoundedSetOfEndpoints(t *testing.T) {
	var d demoSlot
	for p := range maxDemoEndpoints + 5 {
		d.remember(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(1000+p)))
	}
	d.remember(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 1000+maxDemoEndpoints+4)) // already known
	if len(d.used) != maxDemoEndpoints || d.used[0].Port() != 1005 {
		t.Fatalf("%d endpoints, oldest %v", len(d.used), d.used[0])
	}
}

// StopDemo detaches the demo session and the demo together: a Start that
// races it either fails (the demo session is still there) or starts a
// session that is not marked demo (the demo is gone). Never a demo session
// to a stopped demo.
func TestDemo_StopDemoIsAtomicWithStart(t *testing.T) {
	const trials = 50
	eng := NewEngineWith(Options{DemoListenPacket: memnet.New().Host("127.0.0.1")})
	defer eng.Close()
	for trial := range trials {
		out, err := eng.StartDemo(`{"version":0,"family":"xair"}`)
		if err != nil {
			t.Fatal(err)
		}
		var m demoMixerJSON
		_ = decodeStrict(out, &m)
		if _, err := eng.Start(demoConfig(m)); err != nil {
			t.Fatal(err)
		}
		stopped := make(chan error, 1)
		go func() { stopped <- eng.StopDemo() }()
		for {
			_, err := eng.Start(demoConfig(m))
			if CodeOf(err) != CodeAlreadyStarted {
				break
			}
			runtime.Gosched()
		}
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		eng.mu.Lock()
		eng.demo.mu.Lock()
		orphan := eng.sup != nil && eng.demo.session.Load() && eng.demo.mixer == nil
		eng.demo.mu.Unlock()
		eng.mu.Unlock()
		if orphan {
			t.Fatalf("trial %d: a session marked demo outlives StopDemo", trial)
		}
		_ = eng.Stop()
	}
}

// demoVox1 is input 10, "Vox 1": fader -2 dB, headamp 10 at +32.5 dB.
const (
	demoVox1Fader = `{"strip":{"kind":"input","index":10},"field":"fader"}`
	demoVox1Gain  = `{"strip":{"kind":"headamp","index":10},"field":"gain"}`
	demoVox1DB    = 32.5
)

// The demo shows Gain: every input's headamp is seeded (x32: local 1..32,
// playback and click come from the card and have none).
func assertDemoHeadamps(t *testing.T, st stateJSON, fam string) {
	t.Helper()
	var vox *headampJSON
	for i, h := range st.Headamps {
		if h.Index == 10 {
			vox = &st.Headamps[i]
		}
	}
	if vox == nil || vox.GainDB != demoVox1DB || !vox.Writable {
		t.Fatalf("%s: headamp 10 %+v, want +%v dB writable", fam, vox, demoVox1DB)
	}
	if fam == familyX32 && string(st.Strips[14].Headamp) != "null" {
		t.Fatalf("x32 Playback L headamp %s, want null (card)", st.Strips[14].Headamp)
	}
}

// A demo session goes through the same safety path as a console: a fader
// push is never stopped (v0.5, the slip guard is retired), a Gain hold's
// window is the headamp's range and one write moves at most 6 dB.
func assertDemoSafetyPath(t *testing.T, d *demoEnv) {
	t.Helper()
	gesture := func(typ, param, extra string) gestureResultJSON {
		t.Helper()
		got, err := d.eng.Gesture(cmdJSON(typ, param, extra))
		var r gestureResultJSON
		if err != nil || json.Unmarshal([]byte(got), &r) != nil {
			t.Fatalf("%s %s: %s %v", typ, param, got, err)
		}
		return r
	}
	hold := gesture("holdBegin", demoVox1Fader, "")
	if hold.Window == nil {
		t.Fatalf("fader window %+v", hold.Window)
	}
	if r := gesture("set", demoVox1Fader, `,"value":10`); r.State != "free" || r.Value.(float64) != 10 {
		t.Fatalf("fader +10 dB in a hold: %+v, want free at +10 (no wall since v0.5)", r)
	}
	gesture("holdEnd", demoVox1Fader, "")
	if g := gesture("holdBegin", demoVox1Gain, ""); g.Window == nil || g.Window.CapUpDB == nil || *g.Window.CapUpDB != 59.5 {
		t.Fatalf("Gain window %+v, want the headamp's range up to +59.5", g.Window)
	}
	if r := gesture("set", demoVox1Gain, `,"value":59`); r.State != "free" || r.Value.(float64) != demoVox1DB+6 || !r.Written {
		t.Fatalf("Gain candidate +59 dB: %+v, want +%v (6 dB from the previous write)", r, demoVox1DB+6)
	}
	gesture("holdEnd", demoVox1Gain, "")
	time.Sleep(echoWait)
	synctest.Wait()
	if c := findChange(d.rec.all(), "headamp", 10, "gain"); c == nil || c.Value.(float64) != demoVox1DB+6 {
		t.Fatalf("Gain echo %+v", c)
	}
}
