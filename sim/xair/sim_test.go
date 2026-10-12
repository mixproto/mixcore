// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// XA-F10, XA-F16, XA-F70: identity replies, V2 model strings included.
func TestSim_XinfoInfoAndStatus(t *testing.T) {
	for _, model := range []string{"XR18", "X18", "MR18V2", "XR12W"} {
		synctest.Test(t, func(t *testing.T) {
			h := runSim(t, Options{Name: "XR18-1D-DA-B4", Model: model, Firmware: "1.22"})
			c := h.client("10.0.0.2")
			c.send("/xinfo")
			m, ok := c.recv(time.Second)
			if !ok || !slices.Equal(m.strings, []string{"10.0.0.1", "XR18-1D-DA-B4", model, "1.22"}) {
				t.Fatalf("/xinfo = %+v", m)
			}
			c.send("/info")
			m, _ = c.recv(time.Second)
			if !slices.Equal(m.strings, []string{"V0.04", "XR18-1D-DA-B4", model, "1.22"}) {
				t.Fatalf("/info = %+v (second field is the device name, XA-F10)", m)
			}
			c.send("/status")
			m, _ = c.recv(time.Second)
			if !slices.Equal(m.strings, []string{"active", "10.0.0.1", "XR18-1D-DA-B4"}) {
				t.Fatalf("/status = %+v", m)
			}
		})
	}
}

// Section 3: unpadded bus, FX return, FX send and DCA indexes, /lr for main,
// two-digit send slots, no main membership and no send on/off.
func TestSim_AddressTree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		for _, addr := range []string{
			"/ch/01/mix/fader", "/ch/16/config/name", "/ch/16/grp/dca", "/rtn/aux/mix/on", "/rtn/2/mix/on",
			"/rtn/4/grp/mute", "/bus/3/mix/fader", "/bus/6/grp/dca", "/fxsend/4/mix/on", "/lr/mix/fader", "/lr/mix/on",
			"/dca/4/fader", "/dca/4/on", "/config/mute/4", "/ch/05/mix/03/level", "/ch/01/mix/10/level",
			"/rtn/1/mix/07/level", "/ch/01/mix/02/tap", "/-snap/index", "/-snap/64/name", "/config/chlink/15-16",
		} {
			if _, ok := c.read(addr); !ok {
				t.Errorf("%s did not answer", addr)
			}
		}
		for _, addr := range []string{
			"/bus/03/mix/fader", "/rtn/02/mix/on", "/main/st/mix/fader", "/lr/grp/mute", "/lr/grp/dca",
			"/ch/01/mix/01/on", "/ch/17/mix/fader", "/bus/7/mix/fader", "/config/mute/5", "/dca/5/on", "/dca/1/mix/fader",
			"/ch/01/mix/11/level",
		} {
			if _, ok := c.read(addr); ok {
				t.Errorf("%s answered; it is not in the X Air tree", addr)
			}
		}
		if n := len(h.sim.Writes()); n != 0 {
			t.Fatalf("reads logged as %d writes", n)
		}
	})
}

// XA-F72, test item 12: a small-model variant without channel 16 and bus 6.
func TestSim_SmallModelOption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{Model: "XR12", InputChannels: 12, Buses: 2})
		c := h.client("10.0.0.2")
		for addr, want := range map[string]bool{
			"/ch/12/mix/fader": true, "/ch/13/mix/fader": false, "/ch/16/mix/on": false,
			"/bus/2/mix/fader": true, "/bus/3/mix/fader": false, "/bus/6/mix/on": false,
			"/ch/01/mix/02/level": true, "/ch/01/mix/06/level": false, "/ch/01/mix/07/level": true,
			"/fxsend/4/mix/fader": true, "/rtn/4/mix/fader": true,
		} {
			if _, ok := c.read(addr); ok != want {
				t.Errorf("%s answered = %t, want %t", addr, ok, want)
			}
		}
	})
}

// XA-F40, XA-F41, section 3.5: the documented fader packet, 1024 fader steps
// and 161 send steps (test item 8: 0.7531 is stored as 0.75).
func TestSim_FaderAndSendQuantization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		raw, _ := hex.DecodeString("2f63682f30312f6d69782f6661646572000000002c6600003f400000") // section 3.5
		if _, err := c.pc.WriteTo(raw, simUDP); err != nil {
			t.Fatal(err)
		}
		c.send("/ch/02/mix/fader", osc.Float(0.8250244))
		c.send("/ch/01/mix/01/level", osc.Float(0.7531))
		synctest.Wait()
		for addr, want := range map[string]float32{
			"/ch/01/mix/fader": 767.0 / 1023, "/ch/02/mix/fader": 844.0 / 1023, "/ch/01/mix/01/level": 0.75, // XA-F41 table: 0 dB is step 767
		} {
			if v, _ := h.sim.Value(addr); v.Float != want {
				t.Errorf("%s = %v, want %v", addr, v.Float, want)
			}
		}
	})
}

// XA-F03: wrong types, out-of-range values and unknown addresses are logged
// as writes, not applied, and get no reply.
func TestSim_RejectsBadWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		c.send("/xremote")
		c.send("/ch/01/mix/fader", osc.Int(1))
		c.send("/ch/01/mix/on", osc.Int(2))
		c.send("/ch/01/grp/mute", osc.Int(16))
		c.send("/ch/01/config/name", osc.String("THIRTEEN-CHAR"))
		c.send("/lr/grp/mute", osc.Int(1))
		c.send("/-snap/index", osc.Int(3))
		if got := c.all(200 * time.Millisecond); len(got) != 0 {
			t.Fatalf("bad writes were answered: %+v", got)
		}
		w := h.sim.Writes()
		if len(w) != 6 || slices.ContainsFunc(w, func(e Entry) bool { return e.Accepted }) {
			t.Fatalf("write log = %+v", w)
		}
		if err := h.sim.SurfaceSet("/nope", osc.Int(1)); !errors.Is(err, ErrUnknownAddress) {
			t.Fatalf("SurfaceSet unknown: %v", err)
		}
	})
}

// XA-F22, XA-F23 and section 2 "Echo design": with /xremote the change goes
// back to its sender too (default); H1 and H3 are options; /xremotenfb
// never gets its own change back.
func TestSim_EchoRules(t *testing.T) {
	const addr = "/ch/01/mix/on"
	for _, tc := range []struct {
		name                string
		opts                Options
		subGetsOwn, cmdGets bool
	}{
		{"default H2: push to the sender", Options{}, true, false},
		{"H1: no push to the sender", Options{NoEchoToSender: true}, false, false},
		{"H3: a set is answered to its sender", Options{ReplyToSet: true}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := runSim(t, tc.opts)
				sub, cmd, nfb := h.client("10.0.0.2"), h.client("10.0.0.2"), h.client("10.0.0.3")
				sub.send("/xremote")
				nfb.send("/xremotenfb")
				synctest.Wait()
				sub.send(addr, osc.Int(0))
				if got := len(sub.all(100*time.Millisecond)) == 1; got != tc.subGetsOwn {
					t.Fatalf("subscriber got its own change back = %t", got)
				}
				if got := len(nfb.all(100 * time.Millisecond)); got != 1 {
					t.Fatalf("/xremotenfb client got %d pushes of another's change", got)
				}
				cmd.send(addr, osc.Int(1)) // a port that never sent /xremote
				if got := len(sub.all(100 * time.Millisecond)); got != 1 {
					t.Fatalf("subscriber got %d pushes of another port's change", got)
				}
				if got := len(cmd.all(100*time.Millisecond)) == 1; got != tc.cmdGets {
					t.Fatalf("writer without /xremote got an answer = %t", got)
				}
				nfb.all(100 * time.Millisecond)
				nfb.send(addr, osc.Int(0))
				if got := nfb.all(100 * time.Millisecond); len(got) != 0 && !tc.opts.ReplyToSet {
					t.Fatalf("/xremotenfb got its own change back: %+v", got)
				}
			})
		})
	}
}

// XA-F20, XA-F21: eight push clients; the ninth is ignored by default or
// evicts the oldest (option); a slot frees 10 s after the last renewal.
func TestSim_EightPushClientsAndTheNinth(t *testing.T) {
	for _, evict := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			h := runSim(t, Options{EvictOldestWhenFull: evict})
			var cs []*client
			for range 9 {
				c := h.client("10.0.0.2")
				c.send("/xremote")
				synctest.Wait()
				cs = append(cs, c)
			}
			if err := h.sim.SurfaceSet("/ch/03/mix/fader", osc.Float(0.5)); err != nil {
				t.Fatal(err)
			}
			served := make([]bool, len(cs))
			for i, c := range cs {
				_, served[i] = c.recv(100 * time.Millisecond)
			}
			want := []bool{true, true, true, true, true, true, true, true, false}
			if evict {
				want = []bool{false, true, true, true, true, true, true, true, true}
			}
			if !slices.Equal(served, want) {
				t.Fatalf("evict=%t: served %v, want %v", evict, served, want)
			}
			if got := len(h.sim.Subscribers()); got != DefaultMaxPushClients {
				t.Fatalf("%d subscribers", got)
			}
			time.Sleep(DefaultSubscriptionTTL + time.Millisecond)
			if got := len(h.sim.Subscribers()); got != 0 {
				t.Fatalf("%d subscribers after the 10 s lifetime", got)
			}
		})
	}
}

// XA-F16 option: /status says "standby" to a port without a push slot.
func TestSim_StatusStandbyOption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{StandbyWhenNotServed: true, MaxPushClients: 1})
		a, b := h.client("10.0.0.2"), h.client("10.0.0.3")
		a.send("/xremote")
		b.send("/xremote")
		synctest.Wait()
		for c, want := range map[*client]string{a: "active", b: "standby"} {
			c.send("/status")
			if m, _ := c.recv(time.Second); len(m.strings) == 0 || m.strings[0] != want {
				t.Fatalf("/status = %+v, want %s", m, want)
			}
		}
	})
}

// XA-F50..F53, XA-F56: bank 1 is 40 little-endian int16 values in 1/256 dB
// after a little-endian count, every 50 ms for 10 s; both request forms work.
func TestSim_MetersBank1(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		h.sim.SetMeterValues([]int16{0, -4608})
		h.sim.SetMeterValue(Bank1MainL, -256)
		h.sim.SetMeterValue(Bank1MainL+1, MeterSilence)
		a, b, other := h.client("10.0.0.2"), h.client("10.0.0.3"), h.client("10.0.0.4")
		a.send("/meters", osc.String(Bank1), osc.Int(0)) // the form mixcore sends (XA-F57)
		b.send("/meters", osc.String(Bank1))
		other.send("/meters", osc.String("/meters/2"))
		m, ok := a.recv(time.Second)
		if !ok || m.addr != Bank1 || m.arg.Type != osc.TypeBlob {
			t.Fatalf("meter packet = %+v", m)
		}
		blob := m.arg.Bytes
		if len(blob) != 4+2*bank1Values || binary.LittleEndian.Uint32(blob) != bank1Values {
			t.Fatalf("blob of %d bytes, count %d", len(blob), binary.LittleEndian.Uint32(blob))
		}
		val := func(i int) int16 { return int16(binary.LittleEndian.Uint16(blob[4+2*i:])) }
		if val(0) != 0 || val(1) != -4608 || val(2) != MeterSilence || val(Bank1MainL) != -256 || val(Bank1MainL+1) != MeterSilence {
			t.Fatalf("values %d %d %d main %d %d", val(0), val(1), val(2), val(Bank1MainL), val(Bank1MainL+1))
		}
		if _, ok := b.recv(time.Second); !ok {
			t.Fatal("the ,s form must start the stream too")
		}
		if got := other.all(200 * time.Millisecond); len(got) != 0 {
			t.Fatal("other banks are not modelled")
		}
		n := len(a.all(DefaultMeterTTL))
		if want := int(DefaultMeterTTL / DefaultMeterInterval); math.Abs(float64(n-want)) > 2 {
			t.Fatalf("%d packets in 10 s, want about %d", n, want)
		}
		if got := a.all(time.Second); len(got) != 0 {
			t.Fatal("the stream must stop 10 s after the last request")
		}
		if w := h.sim.Writes(); len(w) != 0 {
			t.Fatal("meter requests are not writes")
		}
	})
}

// XA-F60..F63: loads apply the slot and set /-snap/index; the index push is
// an option; client actions are writes; slot 64 changes only when asked.
func TestSim_Snapshots(t *testing.T) {
	for _, pushIdx := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			h := runSim(t, Options{PushSnapIndex: pushIdx})
			sub := h.client("10.0.0.2")
			sub.send("/xremote")
			synctest.Wait()
			scene := map[string]osc.Arg{"/ch/01/mix/fader": osc.Float(0.5), "/ch/02/mix/on": osc.Int(0), "/lr/mix/fader": osc.Float(0)}
			if err := h.sim.StoreSnapshot(2, "Band", scene); err != nil {
				t.Fatal(err)
			}
			if err := h.sim.LoadSnapshot(2); err != nil {
				t.Fatal(err)
			}
			got := sub.all(100 * time.Millisecond)
			var addrs []string
			for _, m := range got {
				addrs = append(addrs, m.addr)
			}
			want := []string{"/ch/01/mix/fader", "/ch/02/mix/on"} // /lr was already 0: not pushed
			if pushIdx {
				want = append([]string{addrSnapIdx}, want...)
			}
			if !slices.Equal(addrs, want) {
				t.Fatalf("pushIdx=%t: pushes %v, want %v", pushIdx, addrs, want)
			}
			if v, _ := sub.read(addrSnapIdx); v.Int != 2 {
				t.Fatalf("/-snap/index = %d", v.Int)
			}
			if v, _ := sub.read("/-snap/02/name"); string(v.Bytes) != "Band" {
				t.Fatalf("slot 2 name = %q", v.Bytes)
			}
			if h.sim.HasSnapshot(64) || h.sim.LoadSnapshot(64) == nil || h.sim.StoreSnapshot(65, "", nil) == nil {
				t.Fatal("slot 64 is empty and slot 65 does not exist")
			}
		})
	}
}

func TestSim_SnapshotClientActionsAreWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		_ = h.sim.SurfaceSet("/ch/04/mix/fader", osc.Float(0.25))
		c.send("/-snap/save", osc.Int(5))
		synctest.Wait()
		_ = h.sim.SurfaceSet("/ch/04/mix/fader", osc.Float(1))
		c.send("/-snap/load", osc.Int(5))
		c.send("/-snap/delete", osc.Int(5))
		c.send("/-snap/load", osc.Int(65))
		synctest.Wait()
		if v, _ := h.sim.Value("/ch/04/mix/fader"); v.Float != 256.0/1023 {
			t.Fatalf("load of a saved slot: fader %v", v.Float)
		}
		w := h.sim.Writes()
		accepted := []bool{true, true, true, false}
		if len(w) != len(accepted) {
			t.Fatalf("write log %+v", w)
		}
		for i, e := range w {
			if e.Accepted != accepted[i] {
				t.Fatalf("write %d %s accepted=%t", i, e.Address, e.Accepted)
			}
		}
		if h.sim.HasSnapshot(5) {
			t.Fatal("delete must empty the slot")
		}
	})
}

// Pause and Reboot: nothing in or out; a reboot drops every registration.
func TestSim_PauseAndReboot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		c.send("/xremote")
		synctest.Wait()
		h.sim.Pause()
		c.send("/xinfo")
		if _, ok := c.recv(time.Second); ok {
			t.Fatal("paused mixer answered")
		}
		h.sim.Resume()
		h.sim.Reboot(5 * time.Second)
		if len(h.sim.Subscribers()) != 0 || len(h.sim.Slots()) != 0 {
			t.Fatal("reboot must drop /xremote registrations")
		}
		c.send("/xinfo")
		if _, ok := c.recv(time.Second); ok {
			t.Fatal("booting mixer answered")
		}
		time.Sleep(5 * time.Second)
		c.send("/xinfo")
		if _, ok := c.recv(time.Second); !ok {
			t.Fatal("no answer after the boot")
		}
		if h.sim.MaxSlotsUsed() != 1 || len(h.sim.Log()) == 0 || h.sim.WriteCount() != 0 {
			t.Fatal("bookkeeping")
		}
	})
}

// test item 3: with SlotForAnySender a write-only port also takes a slot.
func TestSim_SlotForAnySenderOption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{SlotForAnySender: true})
		h.client("10.0.0.2").send("/ch/01/mix/fader")
		synctest.Wait()
		if s := h.sim.Slots(); len(s) != 1 || s[0].Xremote {
			t.Fatalf("slots %+v", s)
		}
	})
}

// XA section 3.4 and 4, XA-F73: mic headamps 01..16 (fewer on small models),
// gain on 145 steps of 0.5 dB; no line headamps are modelled.
func TestSim_HeadampGain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{MicHeadamps: 8})
		c := h.client("10.0.0.2")
		for addr, want := range map[string]bool{"/headamp/01/gain": true, "/headamp/08/gain": true, "/headamp/09/gain": false, "/headamp/17/gain": false} {
			if _, ok := c.read(addr); ok != want {
				t.Errorf("%s answered = %t", addr, ok)
			}
		}
		c.send("/headamp/02/gain", osc.Float(0.6292)) // between steps 90 and 91 of 144
		synctest.Wait()
		if v, _ := h.sim.Value("/headamp/02/gain"); v.Float != float32(91.0/144) {
			t.Fatalf("stored %v, want step 91", v.Float)
		}
		if v, _ := h.sim.Value("/headamp/01/gain"); v.Float != float32(24.0/144) {
			t.Fatalf("initial gain %v, want 0 dB", v.Float)
		}
	})
}
