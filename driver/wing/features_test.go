// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"math"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestMeters_StreamRenewAndStop (WG-F50..F53, wing.md 6): inputs pre-fader,
// Main L/R last, renewed within the 5 s lifetime; switching off stops
// publishing at once and lets the stream expire.
func TestMeters_StreamRenewAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		r.sim.SetInputLevel(1, -18)
		r.sim.SetMainLevels(-6, -7)
		s := r.connect()
		r.run(s)
		_ = s.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(20 * time.Second) // four lifetimes: only renewals keep it alive
		synctest.Wait()
		var fr meters.Frame
		if !r.slot.CopyLatest(&fr) || fr.Count != meterLevels {
			t.Fatalf("no frame of %d levels: %+v", meterLevels, fr.Count)
		}
		if math.Abs(float64(fr.Levels[0])+18) > 0.01 || !math.IsInf(float64(fr.Levels[1]), -1) && fr.Levels[1] > -100 {
			t.Fatalf("input levels %v %v", fr.Levels[0], fr.Levels[1])
		}
		if math.Abs(float64(fr.Levels[40])+6) > 0.01 || math.Abs(float64(fr.Levels[41])+7) > 0.01 {
			t.Fatalf("main L/R %v %v", fr.Levels[40], fr.Levels[41])
		}
		seq := fr.Seq
		time.Sleep(time.Second)
		synctest.Wait()
		if r.slot.CopyLatest(&fr); fr.Seq <= seq {
			t.Fatal("meters stopped while on")
		}
		_ = s.SetMeters(driver.MeterRequest{})
		synctest.Wait()
		r.slot.CopyLatest(&fr)
		seq = fr.Seq
		time.Sleep(time.Second)
		synctest.Wait()
		if r.slot.CopyLatest(&fr) {
			t.Fatalf("a frame was published after meters were switched off (seq %d -> %d)", seq, fr.Seq)
		}
	})
}

// TestSourceChange_AES50Headamp (wing.md 3.4): a channel patched to AES50
// A 5 reports headamp 105, read-only, and its gain is read (never written).
func TestSourceChange_AES50Headamp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		mustSet(t, r.sim, "/io/in/A/5/g", 12.5)
		s := r.connect()
		if _, ok := s.Snapshot().Strip(model.StripRef{Kind: model.StripHeadamp, Index: 105}); ok {
			t.Fatal("an AES50 headamp no channel uses must not be in the state")
		}
		if h, ok := s.Capabilities().Headamp(105); !ok || h.Writable || h.Bank != model.BankAES50A || h.Number != 5 {
			t.Fatalf("AES50 A 5 capability %+v %v", h, ok)
		}
		rn := r.run(s)
		mustSet(t, r.sim, "/ch/6/in/conn/grp", "A")
		mustSet(t, r.sim, "/ch/6/in/conn/in", 5)
		cs := rn.changes(200 * time.Millisecond)
		hc, ok := findChange(cs, ref(model.StripInput, 6, model.FieldHeadamp))
		if n, _ := hc.Value.Index(); !ok || n != 105 {
			t.Fatalf("ch 6 headamp: %+v", hc)
		}
		gc, ok := findChange(cs, ref(model.StripHeadamp, 105, model.FieldGain))
		if db, _ := gc.Value.DB(); !ok || db != 12.5 {
			t.Fatalf("AES50 gain not read: %+v", gc)
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("a patch change wrote")
		}
	})
}

// TestSceneRecall_Burst (wing.md 7): a recall at the console raises
// SceneRecalled (low) and a re-read; it writes nothing.
func TestSceneRecall_Burst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		reads := countClass(r.sim, simwing.ClassRead)
		r.sim.RecallScene(2)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		var recalled bool
		for _, ev := range rn.allEvents() {
			if sr, ok := ev.(driver.SceneRecalled); ok && sr.Confidence == driver.ConfidenceLow {
				recalled = true
			}
		}
		if !recalled {
			t.Fatal("no SceneRecalled after a burst")
		}
		if countClass(r.sim, simwing.ClassRead)-reads < 100 {
			t.Fatal("no re-read after the recall")
		}
		if len(r.sim.Writes()) != 0 {
			t.Fatal("a recall wrote")
		}
	})
}

// TestFirmwareChange_Rewalks: a reconnect to another firmware walks the
// schema again (WG-F35: the schema differs by firmware).
func TestFirmwareChange_Rewalks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{Firmware: "3.0.6-0-g0:release"})
		l, err := r.drv.OpenLink(t.Context(), driver.Endpoint{Host: consoleIP}, r.opts())
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		if _, err := l.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		mustSet(t, r.sim, "/$syscfg/$firmware", "3.1.0-0-g1:release")
		defs := countClass(r.sim, simwing.ClassDefs)
		s2, err := l.Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if countClass(r.sim, simwing.ClassDefs) == defs || s2.Capabilities().MaxPushClients != 24 {
			t.Fatalf("no re-walk after a firmware change, clients %d", s2.Capabilities().MaxPushClients)
		}
	})
}

// TestDiscovery_Probe (WG-F11).
func TestDiscovery_Probe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{Name: "FOH", Model: "wing-compact", IP: consoleIP})
		target := netip.MustParseAddrPort(consoleIP + ":2222")
		found, err := Probe(t.Context(), r.net.Host(clientIP), []netip.AddrPort{target}, 300*time.Millisecond)
		if err != nil || len(found) != 1 {
			t.Fatalf("probe: %v %+v", err, found)
		}
		f := found[0]
		if f.Endpoint != (driver.Endpoint{Host: consoleIP, Port: DefaultPort}) || f.Identity.Name != "FOH" || f.Identity.Model != "wing-compact" {
			t.Fatalf("found %+v", f)
		}
	})
}
