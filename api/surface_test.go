// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"bytes"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func TestReadMeterFrame_FillsCallerBufferOrReportsSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.sim.SetInputLevels([]float32{1, 0.5})
		e.startLive(t)
		buf := make([]byte, MeterFrameMaxBytes)
		n := e.eng.ReadMeterFrame(buf)
		other := e.eng.MeterFrame() // may be a newer frame: compare the layout, not the levels
		if n != meters.FrameHeaderSize+50*4 || len(other) != n || !bytes.Equal(buf[:8], other[:8]) {
			t.Fatalf("ReadMeterFrame = %d bytes, want a frame shaped like MeterFrame (%d bytes)", n, len(other))
		}
		small := bytes.Repeat([]byte{0xAA}, 10)
		if got := e.eng.ReadMeterFrame(small); got != n || !bytes.Equal(small, bytes.Repeat([]byte{0xAA}, 10)) {
			t.Fatalf("short buffer: got %d (want %d) and the buffer must stay untouched", got, n)
		}
		if got := e.eng.ReadMeterFrame(nil); got != n {
			t.Fatalf("nil buffer: %d", got)
		}
	})
}

// A write from another of our own clients (another clientId through the same
// engine, or a MixLink client) is an external change for a held fader: the
// echo carries the other client's origin, never the holder's.
func TestHold_OtherOwnClientWriteEndsGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		hold := `{"version":0,"type":"holdBegin","param":{"strip":{"kind":"input","index":1},"field":"fader"},"origin":{"kind":"gesture","clientId":"ui"}}`
		if err := e.eng.Apply(hold); err != nil {
			t.Fatal(err)
		}
		tablet := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":-12,"origin":{"kind":"gesture","clientId":"tablet","seq":1}}`
		if err := e.eng.Apply(tablet); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		d := e.rec.last()
		if len(d.Events) != 1 || d.Events[0].Type != "externalChangeOnHeld" || *d.Events[0].ClientID != "ui" ||
			d.Events[0].Origin.ClientID != "tablet" || d.Events[0].Origin.Kind != "gesture" {
			t.Fatalf("events = %+v, want ui's gesture ended by the tablet", d.Events)
		}
		if err := e.eng.Apply(cmd("fader", "-4", "gesture")); CodeOf(err) != CodeGestureEnded {
			t.Fatalf("ui's next write = %v, want %s", err, CodeGestureEnded)
		}
	})
}

func TestIdleEngine_RereadsInTheBackgroundButNeverWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		time.Sleep(time.Hour)
		synctest.Wait()
		// The supervisor asks every 15 s; a pass starts 15 s after the
		// previous one ended, so ch 1 is read every 15 s + one pass, never
		// back to back. Since core-api v0.4 a pass is every v0.3 parameter
		// and one slice of the sends (RereadPassLen) at 16 reads per second.
		var at []time.Time
		for _, entry := range e.sim.Log() {
			if entry.Class == simx32.ClassRead && entry.Address == "/ch/01/mix/fader" {
				at = append(at, entry.At)
			}
		}
		const readsPerPass = 16 // per second, driver/internal/xosc ReadWindow (X32-F07)
		pass := time.Duration(x32.New(x32.Config{}).RereadPassLen()/readsPerPass) * time.Second
		minEvery, maxEvery := console.DefaultRereadEvery+pass-10*time.Second, console.DefaultRereadEvery+pass+10*time.Second
		if len(at) < int(time.Hour/maxEvery) {
			t.Fatalf("ch1 fader read %d times in an hour, want at least %d", len(at), int(time.Hour/maxEvery))
		}
		for i := 2; i < len(at); i++ { // at[0] is the connect read
			if d := at[i].Sub(at[i-1]); d < minEvery || d > maxEvery {
				t.Fatalf("read %d came %v after the previous, want %v..%v", i, d, minEvery, maxEvery)
			}
		}
		if n := len(e.sim.Writes()); n != 0 {
			t.Fatalf("an idle engine wrote %d times", n) // safety rule 2
		}
		if st := e.rec.last(); st.Status != "live" {
			t.Fatalf("status %q after an hour", st.Status)
		}
	})
}

func TestReread_LostPushReachesTheUIAsConsoleChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.startLive(t)
		e.net.SetDropFrom(consoleIP, true)
		_ = e.sim.SurfaceSet("/ch/07/mix/fader", osc.Float(0.5)) // -10 dB, push lost
		e.net.SetDropFrom(consoleIP, false)
		time.Sleep(console.DefaultRereadEvery + 10*time.Second)
		synctest.Wait()
		var got *changeJSON
		for _, m := range e.rec.all() {
			if m.Changes == nil {
				continue
			}
			for _, c := range *m.Changes {
				if c.Param.Strip.Index == 7 && c.Param.Field == "fader" {
					got = &c
				}
			}
		}
		if got == nil || got.Origin.Kind != "console" || math.Abs(got.Value.(float64)+10) > 0.05 {
			t.Fatalf("lost push not recovered: %+v", got)
		}
		if len(e.sim.Writes()) != 0 {
			t.Fatal("recovering a lost push must not write")
		}
	})
}
