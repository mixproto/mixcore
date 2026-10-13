// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"math"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

const wait = 50 * time.Millisecond

// TestDocVectors replays the byte strings of wing.md section 3.6.
func TestDocVectors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		p.send("DF D1 D7 38AE75C2 DC") // read ch 1 fader
		if got := p.recv(wait); !equalBytes(got, mustHex(t, "D7 38AE75C2 D5 C3100000")) {
			t.Fatalf("ch 1 fader at -oo: got % X (vendor capture D7 38AE75C2 D5 C3100000, WG-F60)", got)
		}
		p.send("D7 38AE75C2 D5 C1200000 D7 F50F69F8 01 D7 C6AD548D 83 4B69636B") // -10 dB, mute on, name Kick
		_ = p.recv(wait)
		for path, want := range map[string]any{"/ch/1/fdr": -10.0, "/ch/1/mute": 1, "/ch/1/name": "Kick", "/ch/1/$name": "Kick"} {
			if got, _ := r.sim.Value(path); got != want {
				t.Errorf("%s = %v, want %v", path, got, want)
			}
		}
		p.send("D7 DF DE 6A 06 E2 DC") // /$ctl/lib/$active, the DF of its hash escaped
		if got := pushes(t, p.recv(wait)); len(got) != 1 || got[0xDF6A06E2].kind != tkText {
			t.Fatalf("$active read: %+v", got)
		}
	})
}

func TestDefinitions_RootAndChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		p.send("DFD1 DA DD")
		names, hashes, ended := defNames(t, p.recv(wait))
		if !ended || !slices.Contains(names, "ch") || hashes["ch"] != 0x70B10139 || !slices.Contains(names, "$syscfg") {
			t.Fatalf("root children %v ended %v", names, ended)
		}
		p.send("D7 70B10139 DD")
		names, _, _ = defNames(t, p.recv(wait))
		if len(names) != channels || names[0] != "1" || names[39] != "40" {
			t.Fatalf("/ch children: %d %v", len(names), names[:min(3, len(names))])
		}
	})
}

func TestWrite_PushRulesAndEcho(t *testing.T) {
	for _, echo := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, Options{EchoToWriter: echo})
			a, b := r.dial(), r.dial()
			a.send("DFD1 D7 38AE75C2 D5 C1200000")
			gotA, gotB := pushes(t, a.recv(wait)), pushes(t, b.recv(wait))
			if _, ok := gotA[0x38AE75C2]; ok != echo {
				t.Fatalf("echo %v: writer got its own value back: %v", echo, ok)
			}
			if gotB[0x38AE75C2].f != -10 || gotB[0x7E463474].f != -10 {
				t.Fatalf("other client: fdr and derived $fdr (WG-F24) expected, got %+v", gotB)
			}
			if ws := r.sim.Writes(); len(ws) != 1 || ws[0].Path != "/ch/1/fdr" || !ws[0].Applied {
				t.Fatalf("write log %+v", ws)
			}
		})
	}
}

func TestSurfaceSet_PushesToEveryone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		a := r.dial()
		if err := r.sim.SurfaceSet("/ch/1/mute", 1); err != nil {
			t.Fatal(err)
		}
		if got := pushes(t, a.recv(wait)); got[0xF50F69F8].i != 1 || got[0x26855CD3].i != 1 {
			t.Fatalf("desk mute push: %+v", got)
		}
		if r.sim.WriteCount() != 0 {
			t.Fatal("a desk change is not a client write")
		}
	})
}

func TestConnectionLimit(t *testing.T) {
	for _, tc := range []struct {
		firmware string
		limit    int
	}{{"3.0.6-0-g0:release", 16}, {"3.1.0-0-g0:release", 24}} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, Options{Firmware: tc.firmware})
			for range tc.limit {
				r.dial()
			}
			synctest.Wait()
			extra := r.dial()
			if !extra.closed(wait) || r.sim.Refused() != 1 || r.sim.Connections() != tc.limit {
				t.Fatalf("%s: connection %d not refused (open %d)", tc.firmware, tc.limit+1, r.sim.Connections())
			}
		})
	}
}

func TestIdleTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		p.send("DFD1")
		if p.closed(9 * time.Second) {
			t.Fatal("closed before 10 s of silence")
		}
		if !p.closed(2 * time.Second) {
			t.Fatal("still open after 10 s without a byte (WG-F25)")
		}
	})
}

func TestTCPLock(t *testing.T) {
	for _, answersOld := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, Options{TCPLockAnswersOld: answersOld})
			if err := r.sim.SurfaceSet("/$syscfg/tcplock", 1); err != nil {
				t.Fatal(err)
			}
			p := r.dial()
			p.send("DFD1 D7 38AE75C2 D5 C1200000")
			got := pushes(t, p.recv(wait))
			if v, _ := r.sim.Value("/ch/1/fdr"); v != -144.0 {
				t.Fatalf("locked console applied the write: %v", v)
			}
			if _, ok := got[0x38AE75C2]; ok != answersOld {
				t.Fatalf("answers old value %v: got %+v", answersOld, got)
			}
			if ws := r.sim.Writes(); len(ws) != 1 || ws[0].Applied {
				t.Fatalf("write log %+v", ws)
			}
		})
	}
}

func TestDataEndsWithDEOption(t *testing.T) {
	for _, de := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, Options{DataEndsWithDE: de})
			p := r.dial()
			p.send("DFD1 D7 38AE75C2 DC")
			got := p.recv(wait)
			if (got[len(got)-1] == 0xDE) != de {
				t.Fatalf("DE after data %v: % X", de, got)
			}
		})
	}
}

func TestLaws_LocalGainAndRounding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{RoundFaderDB: 0.1})
		h, _ := r.sim.Hash("/io/in/LCL/1/g")
		p := r.dial()
		p.send("DFD1 D7 " + hashHex(h) + " D5 " + floatBits(11.0)) // 2.5 dB grid (WG-F62): 11 -> 10
		p.send("D7 38AE75C2 D5 " + floatBits(-3.33))
		_ = p.recv(wait)
		if g, _ := r.sim.Value("/io/in/LCL/1/g"); g != 10.0 {
			t.Fatalf("LCL gain %v, want 10 on the 2.5 dB grid", g)
		}
		if f, _ := r.sim.Value("/ch/1/fdr"); math.Abs(f.(float64)+3.3) > 1e-4 {
			t.Fatalf("fader %v, want -3.3 (rounded to 0.1 dB, item 10)", f)
		}
	})
}

func TestDynamicSlotKeepsHash(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		slot, _ := r.sim.Hash("/ch/1/dyn/thr")
		mdl, _ := r.sim.Hash("/ch/1/dyn/mdl")
		p := r.dial()
		p.send("DFD1 D7 " + hashHex(mdl) + " 82 455850") // "EXP"
		_ = p.recv(wait)
		dyn, _ := r.sim.Hash("/ch/1/dyn")
		p.send("D7 " + hashHex(dyn) + " DD")
		_, hashes, _ := defNames(t, p.recv(wait))
		if hashes["exthr"] != slot {
			t.Fatalf("slot renamed with the model must keep its hash (WG-F34): %v", hashes)
		}
	})
}

func TestSceneGo(t *testing.T) {
	for _, markers := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, Options{PushSceneMarkers: markers})
			idx, _ := r.sim.Hash("/$ctl/lib/$actionidx")
			act, _ := r.sim.Hash("/$ctl/lib/$action")
			other := r.dial()
			p := r.dial()
			p.send("DFD1 D7 " + hashHex(idx) + " 02 D7 " + hashHex(act) + " 81 474F") // $actionidx 2, $action "GO"
			got := pushes(t, other.recv(wait))
			if len(got) < sceneChannels {
				t.Fatalf("scene burst of %d pushes", len(got))
			}
			actidx, _ := r.sim.Hash("/$ctl/lib/$actidx")
			if _, ok := got[actidx]; ok != markers {
				t.Fatalf("markers %v: $actidx pushed %v", markers, ok)
			}
		})
	}
}

func hashHex(h uint32) string {
	return string([]byte{hexDigit(h >> 28), hexDigit(h >> 24), hexDigit(h >> 20), hexDigit(h >> 16),
		hexDigit(h >> 12), hexDigit(h >> 8), hexDigit(h >> 4), hexDigit(h)})
}

func hexDigit(v uint32) byte { return "0123456789ABCDEF"[v&0xF] }

// TestUndefinedTokenBytesDoNotPanic: E0..FF are ignored by the simulator.
func TestUndefinedTokenBytesDoNotPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		p.send("DFD1 E0 FF D7 38AE75C2 DC")
		if got := pushes(t, p.recv(wait)); len(got) != 1 {
			t.Fatalf("read after junk: %+v", got)
		}
	})
}

// TestNavigationByNameAndIndex: DA, names, indexes and DB select nodes
// (WG-F30, WG-F31); D8 toggles and D9 steps are applied and logged as writes.
func TestNavigationByNameAndIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		// DA, "ch" (C1 6368), index 2 (41), "mute" (C3 6D757465), D8 toggle.
		p.send("DFD1 DA C1 6368 41 C3 6D757465 D8")
		// DB up to /ch/2, "fdr" (C2 666472), DC: read it.
		p.send("DB C2 666472 DC")
		got := pushes(t, p.recv(wait))
		fdr, _ := r.sim.Hash("/ch/2/fdr")
		if v, _ := r.sim.Value("/ch/2/mute"); v != 1 || got[fdr].kind != tkFloat {
			t.Fatalf("toggle by navigation: mute %v, replies %+v", v, got) // $mute is pushed to everyone too (WG-F24)
		}
		h, _ := r.sim.Hash("/ch/2/pan")
		p.send("D7 " + hashHex(h) + " D9 05") // five steps of 1 (201 steps over -100..100)
		_ = p.recv(wait)
		if v, _ := r.sim.Value("/ch/2/pan"); v != 5.0 {
			t.Fatalf("step: pan %v", v)
		}
		if n := r.sim.WriteCount(); n != 2 {
			t.Fatalf("writes %d", n)
		}
		p.send("D7 " + hashHex(h) + " D6 3F800000") // raw 1.0: the top of the law
		_ = p.recv(wait)
		if v, _ := r.sim.Value("/ch/2/pan"); v != 100.0 {
			t.Fatalf("raw: pan %v", v)
		}
	})
}

// TestReboot_RefusesWhileDown: a reboot drops connections and refuses new
// ones until it is back (section 12 "Faults").
func TestReboot_RefusesWhileDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		r.sim.Reboot(5 * time.Second)
		if !p.closed(wait) {
			t.Fatal("connection survived the reboot")
		}
		if !r.dial().closed(wait) || r.sim.Refused() != 1 {
			t.Fatal("a connection was accepted while down")
		}
		time.Sleep(5 * time.Second)
		q := r.dial()
		q.send("DFD1 D7 38AE75C2 DC")
		if len(pushes(t, q.recv(wait))) != 1 || r.sim.MaxConnectionsUsed() != 1 {
			t.Fatal("not back after the reboot")
		}
	})
}

// TestPause_StallsWithoutClosing: a paused console answers nothing and
// keeps the connection past the idle timeout.
func TestPause_StallsWithoutClosing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, Options{})
		p := r.dial()
		r.sim.Pause()
		p.send("DFD1 D7 38AE75C2 DC")
		if got := p.recv(wait); len(got) != 0 {
			t.Fatalf("a paused console answered % X", got)
		}
		if p.closed(12 * time.Second) {
			t.Fatal("a stalled console closed the connection")
		}
		r.sim.Resume()
		p.send("D7 38AE75C2 DC")
		if len(pushes(t, p.recv(wait))) != 1 {
			t.Fatal("no answer after Resume")
		}
	})
}
