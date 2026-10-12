// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"bytes"
	"errors"
	"math"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// dbTolerance covers the simulators' 1023-step fader quantization (X32-F41).
const dbTolerance = 0.1

var law = x32.New(x32.Config{}).FaderLaw() // xair has the same law (XA-F40)

func faderDB(t *testing.T, c console, addr string) float64 {
	t.Helper()
	v, ok := c.Value(addr)
	if !ok {
		t.Fatalf("%s is not modelled", addr)
	}
	return law.DBFromPosition(float64(v.Float))
}

func TestNewConsole_SeedsTheShow(t *testing.T) {
	for _, fam := range []string{FamilyX32, FamilyXAir} {
		t.Run(fam, func(t *testing.T) {
			sim, _, err := newConsole(fam, law, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := range families[fam].inputs {
				c, root := channels[i], chRoot(i+1)
				if v, _ := sim.Value(root + "/config/name"); string(v.Bytes) != c.name {
					t.Fatalf("ch %d name %q, want %q", i+1, v.Bytes, c.name)
				}
				if got := faderDB(t, sim, root+"/mix/fader"); math.IsInf(c.faderDB, -1) != math.IsInf(got, -1) ||
					(!math.IsInf(got, -1) && math.Abs(got-c.faderDB) > dbTolerance) {
					t.Fatalf("ch %d fader %v dB, want %v", i+1, got, c.faderDB)
				}
				if v, _ := sim.Value(root + "/mix/on"); (v.Int == 0) != c.muted { // X32-F33: 0 = muted
					t.Fatalf("ch %d on = %d, muted %v", i+1, v.Int, c.muted)
				}
				if v, _ := sim.Value(root + "/grp/mute"); v.Int != int32(c.groups) {
					t.Fatalf("ch %d mute groups %b", i+1, v.Int)
				}
			}
			if v, _ := sim.Value("/config/mute/4"); v.Int != 1 { // Speech engaged (X32-F35)
				t.Fatal("mute group 4 must be engaged")
			}
			if v, _ := sim.Value("/config/mute/1"); v.Int != 0 {
				t.Fatal("mute group 1 must be released")
			}
			if got := faderDB(t, sim, families[fam].mainFader); math.Abs(got-mainFaderDB) > dbTolerance {
				t.Fatalf("main %v dB", got)
			}
		})
	}
}

func TestNewConsole_XAirMonitorMixesAndColours(t *testing.T) {
	sim, _, err := newConsole(FamilyXAir, law, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := sim.Value("/ch/01/config/color"); v.Int != colourRed {
		t.Fatalf("kick colour %d", v.Int)
	}
	if v, _ := sim.Value("/ch/07/mix/pan"); math.Abs(float64(v.Float)-0.15) > 0.01 { // -0.7 -> L70
		t.Fatalf("Gtr L pan %v", v.Float)
	}
	if v, _ := sim.Value("/bus/1/config/name"); string(v.Bytes) != "Drum IEM" {
		t.Fatalf("bus 1 %q", v.Bytes)
	}
	if v, _ := sim.Value("/dca/3/config/name"); string(v.Bytes) != "Vox" {
		t.Fatalf("dca 3 %q", v.Bytes)
	}
	sends := []struct {
		addr string
		db   float64
	}{
		{"/ch/01/mix/01/level", sendMeDB},    // kick in the drummer's mix
		{"/ch/10/mix/01/level", sendVoiceDB}, // vocals in every mix
		{"/ch/06/mix/01/level", sendBandDB},
		{"/ch/13/mix/06/level", sendMeDB}, // the pastor's own mic
	}
	for _, s := range sends {
		if got := faderDB(t, sim, s.addr); math.Abs(got-s.db) > 0.5 { // 161 send steps (XA-F41)
			t.Fatalf("%s = %v dB, want %v", s.addr, got, s.db)
		}
	}
	if v, _ := sim.Value("/ch/05/mix/04/tap"); v.Int != xairTapPre {
		t.Fatalf("monitor sends must be pre-fader, tap %d", v.Int)
	}
}

func TestStart_RejectsUnknownFamilyAndMissingLaw(t *testing.T) {
	for _, cfg := range []Config{{Family: "wing", Law: law}, {Family: FamilyX32}} {
		if _, err := Start(cfg); !errors.Is(err, ErrFamily) {
			t.Fatalf("%+v: %v", cfg, err)
		}
	}
}

func TestStart_ListenFailureIsErrListen(t *testing.T) {
	notLoopback := memnet.New().Host("10.0.0.5")
	if _, err := Start(Config{Family: FamilyX32, Law: law, ListenPacket: notLoopback}); !errors.Is(err, ErrListen) ||
		!errors.Is(err, ErrNotLoopback) {
		t.Fatalf("err = %v", err)
	}
}

// client is an OSC client on the in-memory network.
type client struct {
	t  *testing.T
	pc net.PacketConn
	to net.Addr
}

func newClient(t *testing.T, n *memnet.Network, m *Mixer) *client {
	t.Helper()
	pc, err := n.ListenPacket("127.0.0.2:5000") // another loopback address
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return &client{t: t, pc: pc, to: net.UDPAddrFromAddrPort(m.Endpoint())}
}

func (c *client) send(addr string, args ...osc.Arg) {
	c.t.Helper()
	pkt, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.pc.WriteTo(pkt, c.to); err != nil {
		c.t.Fatal(err)
	}
}

// recv returns the next message, or false after wait.
func (c *client) recv(wait time.Duration) (*osc.Message, []byte, bool) {
	c.t.Helper()
	buf := make([]byte, 2048)
	_ = c.pc.SetReadDeadline(time.Now().Add(wait))
	n, _, err := c.pc.ReadFrom(buf)
	if err != nil {
		return nil, nil, false
	}
	var m osc.Message
	if err := osc.Decode(buf[:n], &m); err != nil {
		c.t.Fatal(err)
	}
	return &m, buf[:n], true
}

func startMem(t *testing.T, cfg Config) (*memnet.Network, *Mixer) {
	t.Helper()
	n := memnet.New()
	cfg.Law, cfg.ListenPacket = law, n.Host(loopbackIP)
	m, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return n, m
}

// The demo answers like a console on loopback, its meters move, and Stop
// ends every goroutine (synctest fails on a leftover) and frees the port.
func TestStart_ServesMovingMetersAndStops(t *testing.T) {
	for _, fam := range []string{FamilyX32, FamilyXAir} {
		t.Run(fam, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, m := startMem(t, Config{Family: fam})
				defer m.Stop()
				if m.Endpoint().Addr() != netip.MustParseAddr(loopbackIP) || m.Family() != fam {
					t.Fatalf("endpoint %v family %s", m.Endpoint(), m.Family())
				}
				c := newClient(t, n, m)
				c.send("/xinfo")
				msg, _, ok := c.recv(time.Second)
				if name, _ := msg.StringArg(1); !ok || name != consoleName {
					t.Fatalf("/xinfo reply %v", msg)
				}
				c.send("/meters", osc.String("/meters/1"))
				_, first, ok1 := c.recv(time.Second)
				time.Sleep(400 * time.Millisecond)
				_, later, ok2 := c.recv(time.Second)
				if !ok1 || !ok2 || bytes.Equal(first, later) {
					t.Fatal("meters must arrive and move")
				}
				m.Stop()
				m.Stop() // idempotent
				pc, err := n.ListenPacket(m.Endpoint().String())
				if err != nil {
					t.Fatalf("port not freed: %v", err)
				}
				_ = pc.Close()
			})
		})
	}
}

// pushes counts the parameter pushes (not meters) a /xremote client gets
// during d, renewing its subscription (X32-F20: 10 s lifetime).
func (c *client) pushes(d time.Duration) []*osc.Message {
	c.t.Helper()
	const renewEvery = 5 * time.Second
	var out []*osc.Message
	end := time.Now().Add(d)
	for next := time.Now(); time.Now().Before(end); {
		if !time.Now().Before(next) {
			c.send("/xremote")
			next = time.Now().Add(renewEvery)
		}
		wait := time.Until(next)
		if end.Before(next) {
			wait = time.Until(end)
		}
		if msg, _, ok := c.recv(wait); ok {
			out = append(out, msg)
		}
	}
	return out
}

// With desk changes on, the scripted fader moves arrive as pushes (origin
// console for the engine); without, nothing but meters ever changes.
func TestStart_DeskChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, m := startMem(t, Config{Family: FamilyXAir, DeskChanges: true, DeskChangeEvery: time.Second})
		defer m.Stop()
		got := newClient(t, n, m).pushes(2500 * time.Millisecond)
		if len(got) != 2 || !got[0].AddressIs("/ch/10/mix/fader") || !got[1].AddressIs("/ch/09/mix/fader") {
			t.Fatalf("pushes %v, want Vox 1 then Keys", got)
		}
		if f, _ := got[0].FloatArg(0); math.Abs(law.DBFromPosition(float64(f))-(channels[9].faderDB+3)) > dbTolerance {
			t.Fatalf("Vox 1 moved to %v dB", law.DBFromPosition(float64(f)))
		}
	})
	synctest.Test(t, func(t *testing.T) {
		n, m := startMem(t, Config{Family: FamilyX32})
		defer m.Stop()
		if got := newClient(t, n, m).pushes(2 * time.Minute); len(got) != 0 {
			t.Fatalf("the demo changed %d values by itself: %v", len(got), got[0])
		}
	})
}

// The script brings every fader it moves back to where it started.
func TestDeskChange_ScriptReturnsHome(t *testing.T) {
	for _, fam := range []string{FamilyX32, FamilyXAir} {
		sim, out, err := newConsole(fam, law, nil)
		if err != nil {
			t.Fatal(err)
		}
		a := newAnimator(sim, law, families[fam], out, nil)
		before := []float64{faderDB(t, sim, "/ch/07/mix/fader"), faderDB(t, sim, "/ch/09/mix/fader"), faderDB(t, sim, "/ch/10/mix/fader")}
		a.deskChange()
		if got := faderDB(t, sim, "/ch/10/mix/fader"); math.Abs(got-before[2]-3) > dbTolerance {
			t.Fatalf("%s: first move took Vox 1 to %v dB", fam, got)
		}
		for range len(deskScript) - 1 {
			a.deskChange()
		}
		for i, addr := range []string{"/ch/07/mix/fader", "/ch/09/mix/fader", "/ch/10/mix/fader"} {
			if got := faderDB(t, sim, addr); math.Abs(got-before[i]) > dbTolerance {
				t.Fatalf("%s: %s ends at %v dB, started at %v", fam, addr, got, before[i])
			}
		}
	}
}

type lastMeters struct {
	inputs       []float64
	mainL, mainR float64
}

func (l *lastMeters) publish(inputs []float64, mainL, mainR float64) {
	l.inputs, l.mainL, l.mainR = append(l.inputs[:0], inputs...), mainL, mainR
}

// Main L/R follow the console: faders, mutes and mute groups as set now.
func TestAnimator_MainFollowsTheMix(t *testing.T) {
	sim, _, err := newConsole(FamilyX32, law, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := &lastMeters{}
	a := newAnimator(sim, law, families[FamilyX32], out, nil)
	const sung = chorusBar*bar + 0.3
	a.step(sung)
	if len(out.inputs) != x32Inputs || out.mainL > 0 || out.mainL < -30 || math.Abs(out.mainL-out.mainR) > 6 {
		t.Fatalf("inputs %d, main %v / %v dBFS", len(out.inputs), out.mainL, out.mainR)
	}
	if a.mix[12].audible { // Pastor mic, in engaged mute group 4
		t.Fatal("a member of an engaged mute group must not reach the main mix")
	}
	if a.mix[28].audible { // Click, own mute
		t.Fatal("a muted channel must not reach the main mix")
	}
	before := out.mainL
	if err := sim.SurfaceSet("/dca/3/on", osc.Int(0)); err != nil { // mute the Vox DCA (X32-F37)
		t.Fatal(err)
	}
	a.step(sung)
	if out.mainL >= before-1 {
		t.Fatalf("muting the vocals left main at %v dBFS (was %v)", out.mainL, before)
	}
	if err := sim.SurfaceSet("/main/st/mix/on", osc.Int(0)); err != nil {
		t.Fatal(err)
	}
	a.step(sung)
	if !math.IsInf(out.mainL, -1) || !math.IsInf(out.mainR, -1) {
		t.Fatalf("main muted: %v / %v", out.mainL, out.mainR)
	}
}

func TestXAirRaw(t *testing.T) {
	for _, tc := range []struct {
		db   float64
		want int16
	}{{0, 0}, {-12.5, -3200}, {3, 0}, {math.Inf(-1), -32768}, {-200, -32768}, {math.NaN(), -32768}} {
		if got := xairRaw(tc.db); got != tc.want {
			t.Fatalf("xairRaw(%v) = %d, want %d", tc.db, got, tc.want)
		}
	}
}
