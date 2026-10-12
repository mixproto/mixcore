// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func headamp(i int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: i}, Field: f}
}

// aes50Reads lists the AES50 headamp reads the console received
// (/headamp/032../headamp/127).
func aes50Reads(sim *simx32.Sim) []string {
	var out []string
	for _, e := range sim.Log() {
		var n int
		if e.Class == simx32.ClassRead && strings.HasPrefix(e.Address, "/headamp/") {
			if _, err := fmt.Sscanf(e.Address, "/headamp/%03d/", &n); err == nil && n >= localHeadamps {
				out = append(out, e.Address)
			}
		}
	}
	return out
}

// X32-F70..F74, lead decision: at connect the driver reads the local
// headamps (gain, phantom) and /-ha/00..31, and no AES50 headamp unless a
// channel uses it. Index = NNN + 1; every AES50 headamp is described
// (read-only) so a patch change can bring one in.
func TestHeadamp_ConnectReadsLocalAndTheChannelPatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		if err := r.sim.SurfaceSet("/headamp/002/phantom", osc.Int(1)); err != nil {
			t.Fatal(err)
		}
		sess := r.connect()
		c := sess.Capabilities()
		if !c.InputHeadamps || len(c.Headamps) != 128 {
			t.Fatalf("caps: inputHeadamps %v, %d headamps", c.InputHeadamps, len(c.Headamps))
		}
		for idx, want := range map[int]model.HeadampInfo{
			1:   {Index: 1, Bank: model.BankLocal, Number: 1, MinDB: -12, MaxDB: 60, WriteMaxDB: 59.5, StepDB: 0.5, Writable: true, HasPhantom: true},
			37:  {Index: 37, Bank: model.BankAES50A, Number: 5, MinDB: -12, MaxDB: 60, WriteMaxDB: 59.5, StepDB: 0.5},
			128: {Index: 128, Bank: model.BankAES50B, Number: 48, MinDB: -12, MaxDB: 60, WriteMaxDB: 59.5, StepDB: 0.5},
		} {
			if got, ok := c.Headamp(idx); !ok || got != want {
				t.Errorf("headamp %d: %+v", idx, got)
			}
		}
		st := sess.Snapshot()
		hs := st.Headamps()
		if len(hs) != localHeadamps || hs[2] != (model.Headamp{Index: 3, Phantom: true, HasPhantom: true}) {
			t.Fatalf("snapshot headamps %+v", hs)
		}
		if in5, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 5}); in5.Headamp != 5 {
			t.Fatalf("ch 5 headamp %d, want local 5 (index 5)", in5.Headamp)
		}
		if reads := aes50Reads(r.sim); len(reads) != 0 {
			t.Fatalf("AES50 reads at connect: %v", reads)
		}
	})
}

// A channel patched to a stage box at connect: exactly that AES50 headamp
// is read, read-only; a source without a headamp is index 0 (null).
func TestHeadamp_PatchedAES50HeadampReadAtConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		for addr, v := range map[string]int32{"/-ha/04/index": 36, "/-ha/05/index": -1} { // ch 5 = AES50 A 05; ch 6 = card
			if err := r.sim.SurfaceSet(addr, osc.Int(v)); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.sim.SurfaceSet("/headamp/036/gain", osc.Float(72.0/144)); err != nil { // +24 dB
			t.Fatal(err)
		}
		sess := r.connect()
		st := sess.Snapshot()
		if v, ok := st.Value(headamp(37, model.FieldGain)); !ok || !v.Equal(level(t, 24)) {
			t.Fatalf("AES50 A 05 gain %v %v", v, ok)
		}
		in5, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 5})
		in6, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 6})
		if in5.Headamp != 37 || in6.Headamp != 0 {
			t.Fatalf("ch 5 -> %d, ch 6 -> %d", in5.Headamp, in6.Headamp)
		}
		if reads := aes50Reads(r.sim); len(reads) != 1 || reads[0] != "/headamp/036/gain" {
			t.Fatalf("AES50 reads %v, want only /headamp/036/gain", reads)
		}
		err := sess.Apply(driver.Command{Param: headamp(37, model.FieldGain), Value: level(t, 20), Origin: gesture})
		if !errors.Is(err, driver.ErrUnsupported) || r.sim.WriteCount() != 0 {
			t.Fatalf("AES50 write: %v (%d writes)", err, r.sim.WriteCount())
		}
	})
}

// Lead decision: a patch change while live reads the one headamp the
// channel now uses, never all 128; its value arrives as a console change.
func TestHeadamp_PatchChangeReadsOnlyTheNewHeadamp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		if err := r.sim.SurfaceSet("/headamp/040/gain", osc.Float(44.0/144)); err != nil { // +10 dB
			t.Fatal(err)
		}
		if err := r.sim.SurfaceSet("/-ha/07/index", osc.Int(40)); err != nil { // ch 8 to AES50 A 09
			t.Fatal(err)
		}
		var gotIndex, gotGain bool
		for !gotIndex || !gotGain {
			ev, ok := nextOf[driver.ParamChanged](events, time.Second)
			if !ok {
				t.Fatalf("index %v gain %v", gotIndex, gotGain)
			}
			switch ev.Param {
			case ref(8, model.FieldHeadamp):
				n, _ := ev.Value.Index()
				gotIndex = n == 41
			case headamp(41, model.FieldGain):
				gotGain = ev.Value.Equal(level(t, 10)) && ev.Origin.Kind == driver.OriginConsole
			}
		}
		if reads := aes50Reads(r.sim); len(reads) != 1 || reads[0] != "/headamp/040/gain" {
			t.Fatalf("AES50 reads %v, want only /headamp/040/gain", reads)
		}
	})
}

// AC-PS-20 on the X32 law through the write path: the float32 of x32.md's
// reference table, and the echo with the writer's origin.
func TestHeadamp_LocalGainRoundTrip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		for i, pt := range []struct {
			db  float64
			hex uint32
		}{{-12, 0x00000000}, {0, 0x3e2aaaab}, {0.5, 0x3e31c71c}, {24, 0x3f000000}, {59.5, 0x3f7e38e4}} {
			p := headamp(i+1, model.FieldGain)
			if err := sess.Apply(driver.Command{Param: p, Value: level(t, pt.db), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			ev, ok := nextOf[driver.ParamChanged](events, time.Second)
			wire, _ := r.sim.Value(fmt.Sprintf("/headamp/%03d/gain", i))
			if !ok || ev.Param != p || !ev.Value.Equal(level(t, pt.db)) || ev.Origin != gesture || math.Float32bits(wire.Float) != pt.hex {
				t.Errorf("%v dB: wire %08x (want %08x), echo %+v", pt.db, math.Float32bits(wire.Float), pt.hex, ev)
			}
		}
		r.assertWritesAudited()
	})
}

// X32-F73: phantom is read, never written; /ch/NN/preamp/hpon is never
// touched (it is the low cut).
func TestHeadamp_PhantomNeverWrittenHponNeverUsed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		err := sess.Apply(driver.Command{Param: headamp(1, model.FieldPhantom), Value: model.Bool(true), Origin: gesture})
		if !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("phantom write: %v", err)
		}
		for _, a := range r.drv.Addresses() {
			if strings.Contains(a, "hpon") || strings.Contains(a, "preamp/trim") {
				t.Fatalf("driver reads %s", a)
			}
		}
	})
}

// X32-F74 is unverified: a console that does not answer /-ha connects,
// without the input headamp field.
func TestHeadamp_NoHaIndexAnswerMeansNoChannelMapping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var unknown []string
		for ch := range inputChannels {
			unknown = append(unknown, fmt.Sprintf("/-ha/%02d/index", ch))
		}
		r := newRig(t, simx32.Options{UnknownAddresses: unknown})
		sess := r.connect()
		if sess.Capabilities().InputHeadamps {
			t.Fatal("input headamps without /-ha")
		}
		if len(sess.Snapshot().Headamps()) != localHeadamps {
			t.Fatal("local headamps must still be read")
		}
	})
}

// Re-review MEDIUM B: the console layer keeps a Gain write in flight until
// the console reports that parameter. A write lost on the way leaves the
// console unchanged, so the re-read reply equals the last value seen; it
// is still reported (console origin), or the write would stay in flight.
func TestHeadamp_RereadReportsGainWithAnUnconfirmedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		r.net.SetDropFrom(clientIP, true) // the write is lost
		if err := sess.Apply(driver.Command{Param: headamp(1, model.FieldGain), Value: level(t, 6), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		r.net.SetDropFrom(clientIP, false)
		time.Sleep(DefaultTiming.EchoWindow + time.Second) // past the echo window: the pass reads it
		if err := sess.(driver.Rereader).Reread(); err != nil {
			t.Fatal(err)
		}
		for {
			ev, ok := nextOf[driver.ParamChanged](events, passTime)
			if !ok {
				t.Fatal("the re-read never reported headamp 1 Gain")
			}
			if ev.Param == headamp(1, model.FieldGain) {
				if !ev.Value.Equal(level(t, 0)) || ev.Origin.Kind != driver.OriginConsole {
					t.Fatalf("report %+v, want the console's 0 dB", ev)
				}
				break
			}
		}
		if w := r.sim.WriteCount(); w != 0 {
			t.Fatalf("%d writes reached the console", w)
		}
	})
}

// Re-review LOW D: whether writing the wire maximum 1.0 is safe is
// unverified (X32-F72 item 17); +60 dB is refused and nothing is sent, a
// console at +60 dB still reads as +60 dB, and the writable top is +59.5.
func TestHeadamp_WireMaximumReadNeverWritten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		if err := r.sim.SurfaceSet("/headamp/001/gain", osc.Float(1)); err != nil {
			t.Fatal(err)
		}
		sess := r.connect()
		if v, _ := sess.Snapshot().Value(headamp(2, model.FieldGain)); !v.Equal(level(t, 60)) {
			t.Fatalf("read 1.0 as %v", v)
		}
		err := sess.Apply(driver.Command{Param: headamp(1, model.FieldGain), Value: level(t, 60), Origin: gesture})
		synctest.Wait()
		if !errors.Is(err, model.ErrInvalidValue) || r.sim.WriteCount() != 0 {
			t.Fatalf("+60 dB: %v, %d writes", err, r.sim.WriteCount())
		}
		if h, _ := sess.Capabilities().Headamp(1); h.MaxDB != 60 || h.WriteMaxDB != 59.5 {
			t.Fatalf("law max %v, writable max %v", h.MaxDB, h.WriteMaxDB)
		}
	})
}
