// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// sendCmd is a command on input 'input' send to 'bus' (v0.4).
func sendCmd(typ, field string, input, bus int, value, client string) string {
	v := ""
	if value != "" {
		v = `,"value":` + value
	}
	return `{"version":0,"type":"` + typ + `","param":{"strip":{"kind":"input","index":` + itoa(input) + `},"field":"` + field +
		`","bus":` + itoa(bus) + `}` + v + `,"origin":{"kind":"gesture","clientId":"` + client + `","seq":1}}`
}

func busCmd(typ, field string, bus int, value, client string) string {
	v := ""
	if value != "" {
		v = `,"value":` + value
	}
	return `{"version":0,"type":"` + typ + `","param":{"strip":{"kind":"bus","index":` + itoa(bus) + `},"field":"` + field + `"}` + v +
		`,"origin":{"kind":"gesture","clientId":"` + client + `","seq":1}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestParseCommand_SendsNeedTheirBus(t *testing.T) {
	c, err := parseCommand(sendCmd("set", "sendLevel", 4, 2, "4", "ui"))
	if err != nil || c.param != (model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 4}, Field: model.FieldSendLevel, Bus: 2}) {
		t.Fatalf("send level: %+v, %v", c.param, err)
	}
	for _, tc := range []struct {
		name, in, code string
	}{
		{"send without bus", `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"sendLevel"},"value":0,"origin":{"kind":"gesture"}}`, CodeInvalidCommand},
		{"bus on a fader", `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader","bus":2},"value":0,"origin":{"kind":"gesture"}}`, CodeInvalidCommand},
		{"bus 0", sendCmd("set", "sendLevel", 1, 0, "0", "ui"), CodeInvalidCommand},
		{"pan out of range", sendCmd("set", "sendPan", 1, 1, "120", "ui"), CodeInvalidValue},
		{"pan not a number", sendCmd("set", "sendPan", 1, 1, `"left"`, "ui"), CodeInvalidCommand},
		{"unknown tap", sendCmd("set", "sendTap", 1, 1, `"pre"`, "ui"), CodeInvalidCommand},
		{"bus strip 0", busCmd("set", "fader", 0, "0", "ui"), CodeInvalidCommand},
	} {
		if _, err := parseCommand(tc.in); CodeOf(err) != tc.code {
			t.Fatalf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
	for _, in := range []string{
		sendCmd("set", "sendPan", 1, 1, "-40", "ui"), sendCmd("set", "sendTap", 1, 1, `"preFader"`, "ui"),
		sendCmd("toggle", "sendOn", 1, 2, "", "ui"), sendCmd("holdBegin", "sendLevel", 1, 2, "", "ui"),
		busCmd("set", "colour", 1, `"redInverted"`, "ui"), busCmd("set", "fader", 2, "-3", "ui"),
	} {
		if _, err := parseCommand(in); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
	}
}

func TestParseConfig_Role(t *testing.T) {
	base := `{"version":0,"family":"x32","host":"10.0.0.1","role":`
	ok := map[string]console.Role{
		`{"kind":"owner"}`:    {},
		`{"kind":"engineer"}`: {Kind: console.RoleEngineer},
		`{"kind":"viewer"}`:   {Kind: console.RoleViewer},
		`{"kind":"monitor","buses":[{"bus":2},{"bus":3,"monitorCeiling":"off"},{"bus":4,"monitorCeiling":{"headroomDB":3}}]}`: {Kind: console.RoleMonitor,
			Buses: []console.BusGrant{{Bus: 2, Ceiling: &console.MonitorCeiling{HeadroomDB: 6}}, {Bus: 3}, {Bus: 4, Ceiling: &console.MonitorCeiling{HeadroomDB: 3}}}},
	}
	for in, want := range ok {
		c, err := parseConfig(base + in + `}`)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		got, _ := c.role()
		if !sameRole(got, want) {
			t.Fatalf("%s -> %+v, want %+v", in, got, want)
		}
	}
	for _, in := range []string{
		`null`, `{}`, `{"kind":"monitor"}`, `{"kind":"monitor","buses":[]}`, `{"kind":"monitor","buses":[{"bus":2},{"bus":2}]}`,
		`{"kind":"monitor","buses":[{"bus":0}]}`, `{"kind":"monitor","buses":[{"bus":65}]}`, `{"kind":"viewer","buses":[{"bus":1}]}`,
		`{"kind":"monitor","buses":[{"bus":1,"monitorCeiling":{"headroomDB":11}}]}`, `{"kind":"monitor","buses":[{"bus":1,"monitorCeiling":null}]}`,
		`{"kind":"admin"}`, `{"kind":"owner","extra":1}`,
	} {
		if _, err := parseConfig(base + in + `}`); CodeOf(err) != CodeInvalidConfig {
			t.Fatalf("%s: %v, want ERR_INVALID_CONFIG", in, err)
		}
	}
	if c, err := parseConfig(configOK); err != nil {
		t.Fatal(err)
	} else if r, _ := c.role(); r.Kind != console.RoleOwner {
		t.Fatalf("no role is the owner: %+v", r)
	}
}

func sameRole(a, b console.Role) bool {
	if a.Kind != b.Kind || len(a.Buses) != len(b.Buses) {
		return false
	}
	for i := range a.Buses {
		x, y := a.Buses[i], b.Buses[i]
		if x.Bus != y.Bus || (x.Ceiling == nil) != (y.Ceiling == nil) || (x.Ceiling != nil && *x.Ceiling != *y.Ceiling) {
			return false
		}
	}
	return true
}

func (e *env) startLiveWith(t *testing.T, config string) {
	t.Helper()
	if _, err := e.eng.Start(config); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(time.Second)
	synctest.Wait()
	if st := e.rec.last(); st.Status != "live" {
		t.Fatalf("status = %q, want live", st.Status)
	}
}

// AC-SF-01, AC-SF-02, CR-16: the snapshot lists buses (names, colours,
// links), every input's sends with the console's tap words, matrices and
// DCA masters (x32 X32-F80..F89).
func TestSnapshot_X32BusesSendsAndTaps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		for addr, a := range map[string]osc.Arg{
			"/bus/02/config/name": osc.String("Mon 2 Keys"), "/bus/02/config/color": osc.Int(4), "/config/buslink/1-2": osc.Int(1),
			"/ch/04/mix/02/level": osc.Float(128.0 / 160), "/ch/05/mix/02/on": osc.Int(0), "/ch/04/mix/01/type": osc.Int(3),
			"/dca/1/config/name": osc.String("Band"), "/mtx/01/config/name": osc.String("Delays"),
		} {
			if err := e.sim.SurfaceSet(addr, a); err != nil {
				t.Fatal(err)
			}
		}
		e.startLive(t)
		raw := e.rec.raw[len(e.rec.raw)-1]
		for _, want := range []string{
			`"buses":[{"index":1,"kind":"mix","name":"","colour":"off","faderDB":"-inf","mute":false,"linked":true},` +
				`{"index":2,"kind":"mix","name":"Mon 2 Keys","colour":"blue","faderDB":"-inf","mute":false}`,
			`"sends":[{"bus":1,"levelDB":"-inf","on":true,"pan":0,"tap":"preFader"},{"bus":2,"levelDB":2,"on":true},`,
			`"sendFields":{"on":true,"pan":"pair","tap":"pair"}`,
			`"sendTaps":[{"tap":"input","label":"IN/LC"},{"tap":"preEq","label":"\u003c-EQ"},{"tap":"postEq","label":"EQ-\u003e"},` +
				`{"tap":"preFader","label":"PRE"},{"tap":"postFader","label":"POST"},{"tap":"group","label":"GRP"}]`,
			`"matrices":[{"index":1,"name":"Delays","colour":"off","faderDB":"-inf","mute":false},`,
			`"dcas":[{"index":1,"mute":false,"name":"Band","colour":"off","faderDB":"-inf"},`,
		} {
			if !strings.Contains(raw, want) {
				t.Fatalf("snapshot lacks %s\n...%s", want, raw[max(0, len(raw)-2500):])
			}
		}
		st := e.rec.last()
		if len(st.Buses) != 16 || len(st.Strips[4].Sends) != 16 || st.Strips[4].Sends[1].On == nil || *st.Strips[4].Sends[1].On {
			t.Fatalf("bus count %d, ch 5 sends %+v", len(st.Buses), st.Strips[4].Sends)
		}
	})
}

// AC-SF-03, SR-01: a send write reaches only that send; its echo arrives in
// a diff whose param names the bus, with our origin.
func TestApply_SendWriteAndEchoDiff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		if err := e.eng.Apply(sendCmd("set", "sendLevel", 4, 2, "4", "ui")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if w := e.sim.Writes(); len(w) != 1 || w[0].Address != "/ch/04/mix/02/level" {
			t.Fatalf("writes %+v", w)
		}
		found := false
		for _, m := range e.rec.all() {
			if m.Changes == nil {
				continue
			}
			for _, c := range *m.Changes {
				found = found || (c.Param.Field == "sendLevel" && c.Param.Bus == 2 && c.Origin.ClientID == "ui" && c.Value == float64(4))
			}
		}
		if !found {
			t.Fatal("no diff with the echo of the send")
		}
	})
}

// AC-MV-03, SR-09, invariant 4: with a monitor role for bus 2 the core
// refuses ch 1's channel fader and the send to bus 1 with ERR_ROLE and
// writes nothing; the musician's own mix works and meets its ceiling.
func TestRole_MonitorSeatThroughTheAPI(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		_ = e.sim.SurfaceSet("/ch/04/mix/02/level", osc.Float(128.0/160)) // the engineer set +2 dB
		e.startLiveWith(t, `{"version":0,"family":"x32","host":"10.0.0.1","role":{"kind":"monitor","buses":[{"bus":2}]}}`)
		for _, in := range []string{
			cmd("fader", "-3", "gesture"), sendCmd("set", "sendLevel", 1, 1, "0", "mon"),
			busCmd("toggle", "mute", 1, "", "mon"), `{"version":0,"type":"setShowMode","value":true,"origin":{"kind":"gesture","clientId":"mon"}}`,
		} {
			if err := e.eng.Apply(in); CodeOf(err) != CodeRole {
				t.Fatalf("%s: %v, want ERR_ROLE", in, err)
			}
		}
		if _, err := e.eng.Gesture(sendCmd("holdBegin", "sendLevel", 4, 2, "", "mon")); err != nil {
			t.Fatal(err)
		}
		out, err := e.eng.Gesture(sendCmd("set", "sendLevel", 4, 2, "10", "mon"))
		if err != nil || !strings.Contains(out, `"state":"atCeiling"`) || !strings.Contains(out, `"value":8`) || !strings.Contains(out, `"ceilingDB":8`) {
			t.Fatalf("gesture %s, %v; want +2 + 6 = +8 atCeiling", out, err)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if w := e.sim.Writes(); len(w) != 1 || w[0].Address != "/ch/04/mix/02/level" {
			t.Fatalf("writes %+v, want only the musician's own send", w)
		}
	})
}

// xair.md 3.3: no send on/off (sendFields.on false, no on per send); FX
// sends are buses 7..10 of kind fx; pan and tap per send.
func TestSnapshot_XAirSendsHaveNoOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		e.startLive(t)
		st := e.rec.last()
		if st.SendFields == nil || st.SendFields.On || st.SendFields.Pan != "send" || st.SendFields.Tap != "send" {
			t.Fatalf("sendFields %+v", st.SendFields)
		}
		if len(st.Buses) != 10 || st.Buses[6].Kind != "fx" || st.Buses[0].Kind != "mix" || st.Buses[6].Linked != nil {
			t.Fatalf("buses %+v", st.Buses)
		}
		s := st.Strips[0].Sends[1]
		if s.On != nil || s.Pan == nil || s.Tap == "" {
			t.Fatalf("xair send %+v", s)
		}
		if err := e.eng.Apply(sendCmd("toggle", "sendOn", 1, 2, "", "ui")); CodeOf(err) != CodeUnsupported {
			t.Fatalf("toggle sendOn on xair: %v", err)
		}
		if st.SendTaps[0].Label != "IN" || len(st.Matrices) != 0 {
			t.Fatalf("taps %+v, matrices %d", st.SendTaps, len(st.Matrices))
		}
	})
}

// The snapshot of a console without buses is unchanged from v0.3 (no
// buses, sendFields or sendTaps keys): TestStateJSON_Golden pins it.
func TestSnapshot_NoBusesNoKeys(t *testing.T) {
	ex := stateExamples(t)
	for _, key := range []string{`"buses"`, `"sendFields"`, `"sendTaps"`, `"matrices"`, `"sends"`, `"colour"`} {
		if strings.Contains(string(ex[0]), key) {
			t.Fatalf("snapshot without buses has %s", key)
		}
	}
}
