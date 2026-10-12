// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/internal/xosc"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Rule 1: connect reads the X Air tree (section 3) and writes nothing.
func TestConnect_ReadsFullStateAndSendsNoWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{Name: "XR18-1D-DA-B4", Model: "MR18V2", Firmware: "1.22"})
		for addr, a := range map[string]osc.Arg{
			"/ch/03/config/name": osc.String("Vox"), "/ch/03/mix/fader": osc.Float(0.5), "/ch/16/mix/on": osc.Int(0),
			"/lr/mix/fader": osc.Float(0.75), "/lr/mix/on": osc.Int(0), "/ch/03/grp/mute": osc.Int(0b1010),
			"/ch/03/grp/dca": osc.Int(0b0100), "/config/mute/2": osc.Int(1), "/dca/4/on": osc.Int(0),
		} {
			if err := r.sim.SurfaceSet(addr, a); err != nil {
				t.Fatal(err)
			}
		}
		sess := r.connect()
		id := sess.Identity()
		if id.Model != "MR18V2" || id.Name != "XR18-1D-DA-B4" || id.Firmware != "1.22" || id.Address != mixerIP {
			t.Fatalf("identity %+v", id)
		}
		want := model.Capabilities{Headamps: wantHeadamps(16), InputCount: 16, HasMain: true, MuteGroups: 4, DCAs: 4, MainMuteGroups: false,
			InputMeters: true, EchoesOwnWrites: true, MaxPushClients: 8}
		if c := sess.Capabilities(); !reflect.DeepEqual(c, want) {
			t.Fatalf("capabilities %+v, want %+v", c, want)
		}
		st := sess.Snapshot()
		in3, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 3})
		// 0.5 is stored as step 512 = -9.98 dB (XA-F41 table).
		if in3.Name != "Vox" || math.Abs(in3.FaderDB+9.9804) > 1e-3 || in3.MuteGroups != 0b1010 || in3.DCAs != 0b0100 {
			t.Fatalf("input 3 = %+v", in3)
		}
		if in16, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 16}); !in16.Muted {
			t.Fatal("ch 16 mix/on 0 must be muted")
		}
		main, _ := st.Main()
		if math.Abs(main.FaderDB-(767.0/1023*40-30)) > 1e-4 || !main.Muted || main.MuteGroups != 0 { // 0.75 is stored as step 767
			t.Fatalf("main = %+v", main)
		}
		if mg := st.MuteGroups(); len(mg) != 4 || !mg[1].Muted {
			t.Fatalf("mute groups %+v", mg)
		}
		if d := st.DCAs(); len(d) != 4 || !d[3].Muted {
			t.Fatalf("dcas %+v", d)
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("connect wrote %d times", n)
		}
		for _, e := range r.sim.Log() {
			if strings.HasPrefix(e.Address, "/-snap/") || e.Address == "/main/st/grp/mute" || e.Address == "/lr/grp/mute" {
				t.Fatalf("connect read %s, which the driver must never touch", e.Address)
			}
		}
	})
}

// XA section 3.2: main has no mute-group membership; it is not modelled.
func TestMain_HasNoMuteGroupMembership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		if _, ok := r.drv.Address(mainRef(model.FieldMuteGroups)); ok {
			t.Fatal("no address for main muteGroups")
		}
		err := sess.Apply(driver.Command{Param: mainRef(model.FieldMuteGroups), Value: model.Mask(1), Origin: gesture})
		if !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("Apply main muteGroups = %v", err)
		}
		for addr, want := range map[model.ParamRef]string{
			ref(1, model.FieldFader): "/ch/01/mix/fader", ref(16, model.FieldName): "/ch/16/config/name",
			mainRef(model.FieldFader): "/lr/mix/fader", mainRef(model.FieldMute): "/lr/mix/on",
			grp(model.StripMuteGroup, 4): "/config/mute/4", grp(model.StripDCA, 4): "/dca/4/on",
		} {
			if got, _ := r.drv.Address(addr); got != want {
				t.Errorf("%v -> %s, want %s", addr, got, want)
			}
		}
		if n := len(r.drv.Addresses()); n != 16*5+2+4+4+16*2+1 {
			t.Fatalf("%d addresses; 90 reads of the v1 table (xair.md section 12) plus 16 mic gains and phantoms and the aux gain", n)
		}
	})
}

func TestApply_RejectsNonUserOriginAndValidates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		for _, k := range []driver.OriginKind{driver.OriginConsole, driver.OriginSystem} {
			err := sess.Apply(driver.Command{Param: ref(1, model.FieldMute), Value: model.Bool(true), Origin: driver.Origin{Kind: k}})
			if !errors.Is(err, driver.ErrNotUserOrigin) {
				t.Fatalf("origin %v: %v", k, err)
			}
		}
		long, _ := model.Text("THIRTEEN-CHAR")
		if err := sess.Apply(driver.Command{Param: ref(1, model.FieldName), Value: long, Origin: gesture}); !errors.Is(err, model.ErrInvalidValue) {
			t.Fatalf("13-char name: %v (X32 rule kept until test item 14)", err)
		}
		for _, p := range []model.ParamRef{ref(17, model.FieldFader), grp(model.StripMuteGroup, 5), grp(model.StripDCA, 5), ref(1, model.FieldDCAs)} {
			if err := sess.Apply(driver.Command{Param: p, Value: model.Bool(true), Origin: gesture}); !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("%v: %v", p, err)
			}
		}
		synctest.Wait()
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("%d writes from rejected commands", n)
		}
	})
}

// Rule 4 under each echo hypothesis of xair.md section 2: our write comes
// back once, with our origin; a stray echo on the command socket (H3) is
// dropped and never taken for a change at the mixer.
func TestEcho_OwnWriteDoesNotBounceUnderEveryHypothesis(t *testing.T) {
	for name, opts := range map[string]simxair.Options{
		"H2 push to sender (default)": {}, "H1 no push to sender": {NoEchoToSender: true}, "H3 reply to a set": {ReplyToSet: true},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, opts)
				sess := r.connect()
				events, _ := r.run(sess)
				if err := sess.Apply(driver.Command{Param: ref(2, model.FieldFader), Value: level(t, -10), Origin: gesture}); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second)
				var mine, console int
				for _, ev := range drain(events) {
					if pc, ok := ev.(driver.ParamChanged); ok && pc.Param == ref(2, model.FieldFader) {
						switch pc.Origin.Kind {
						case driver.OriginGesture:
							mine++
						default:
							console++
						}
					}
				}
				if mine != 1 || console != 0 {
					t.Fatalf("echo seen %d times with our origin, %d times as a mixer change", mine, console)
				}
				if v, _ := r.sim.Value("/ch/02/mix/fader"); v.Float != 512.0/1023 {
					t.Fatalf("mixer fader %v, want step 512 (-10 dB, XA-F41 table)", v.Float)
				}
				r.assertWritesAudited()
			})
		})
	}
}

// Another app (there is no surface, XA-F74) changes a fader: console origin.
func TestOtherAppChange_ReportedWithConsoleOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		other := r.subscriber("10.0.0.9")
		sendTo(t, other, "/ch/05/mix/on", osc.Int(0))
		ev, ok := nextOf[driver.ParamChanged](events, time.Second)
		if !ok || ev.Param != ref(5, model.FieldMute) || ev.Origin.Kind != driver.OriginConsole || !ev.Value.Equal(model.Bool(true)) {
			t.Fatalf("event %+v, %v", ev, ok)
		}
	})
}

// Rule 2: an idle session only renews and probes; it never writes.
func TestIdleSession_NoWritesForAnHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		_, stop := r.run(sess)
		_ = sess.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(time.Hour)
		if err := stop(); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatalf("%d writes in an idle hour", n)
		}
	})
}

// Rule 3: losing the mixer ends Run with ErrConnectionLost after 1.5 s (XA-F25).
func TestRun_DetectsLostMixer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events := make(chan driver.Event, 16)
		done := make(chan error, 1)
		go func() { done <- sess.Run(context.Background(), events) }()
		time.Sleep(time.Second)
		start := time.Now()
		r.net.SetDown(mixerIP, true)
		err := <-done
		if !errors.Is(err, driver.ErrConnectionLost) {
			t.Fatalf("Run = %v", err)
		}
		// The 1.5 s run from the last packet, which came at most one quiet
		// probe interval (400 ms) before the cut.
		tm := xosc.DefaultTiming
		if d := time.Since(start); d < tm.StaleAfter-tm.QuietProbeAfter || d > tm.StaleAfter+tm.LivenessTick {
			t.Fatalf("lost after %s, want 1.5 s after the last packet", d)
		}
	})
}

// XA-F51..F53: bank 1 int16 values in 1/256 dB become a frame of 16 inputs
// and Main L/R in dBFS; meters keep flowing over the 10 s lifetime.
func TestMeters_Bank1PublishedInDBFS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		r.sim.SetMeterValues([]int16{0, -4608, simxair.MeterSilence, 256})
		r.sim.SetMeterValue(simxair.Bank1MainL, -256)
		r.sim.SetMeterValue(simxair.Bank1MainL+1, -1536)
		r.sim.SetMeterValue(17, 0) // aux R: not in the frame
		sess := r.connect()
		r.run(sess)
		if err := sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		var fr meters.Frame
		if !r.slot.CopyLatest(&fr) || fr.Count != 18 {
			t.Fatalf("frame count %d, want 18", fr.Count)
		}
		want := map[int]float64{0: 0, 1: -18, 2: math.Inf(-1), 3: 1, 4: math.Inf(-1), 15: math.Inf(-1), 16: -1, 17: -6}
		for i, w := range want {
			if got := float64(fr.Levels[i]); got != w {
				t.Errorf("level %d = %v, want %v", i, got, w)
			}
		}
		time.Sleep(25 * time.Second)
		before := fr.Seq
		time.Sleep(time.Second)
		if !r.slot.CopyLatest(&fr) || fr.Seq == before {
			t.Fatal("meters stopped although SetMeters is on (renewal every 3 s, XA-F56)")
		}
		if n := len(r.sim.Writes()); n != 0 {
			t.Fatal("meter requests are not writes")
		}
	})
}

// wantHeadamps describes mic headamps 1..n (XA-F111, XA-F113) and, with
// all 16, the read-only aux 17 on the line law (XA-F110, XA-F112).
func wantHeadamps(n int) []model.HeadampInfo {
	out := make([]model.HeadampInfo, n)
	for i := range out {
		out[i] = model.HeadampInfo{Index: i + 1, Bank: model.BankLocal, Number: i + 1, MinDB: -12, MaxDB: 60, StepDB: 0.5,
			Writable: true, HasPhantom: true}
	}
	if n == 16 {
		out = append(out, model.HeadampInfo{Index: 17, Bank: model.BankAux, Number: 1, MinDB: -12, MaxDB: 20, StepDB: 0.5})
	}
	return out
}
