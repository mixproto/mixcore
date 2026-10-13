// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/backoff"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// Fixture of docs/design/acceptance/phone-safety.md: Vox1 fader -6.0 dB,
// headamp 3 Gain +32.5 dB, range -12..+60 dB in 0.5 dB steps.
var (
	gain3     = model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: 3}, Field: model.FieldGain}
	gainCaps  = model.Capabilities{Headamps: micHeadamps(3), InputCount: 1, HasMain: true}
	macro     = driver.Origin{Kind: driver.OriginMacro, ClientID: "ui", Seq: 1}
	musician  = driver.Origin{Kind: driver.OriginGesture, ClientID: "mon-2", Seq: 1}
	engineerB = driver.Origin{Kind: driver.OriginGesture, ClientID: "foh", Seq: 1}
)

func safetyState(faderDB float64) model.State {
	st, err := stateWith(faderDB).WithHeadamps([]model.Headamp{{Index: 1}, {Index: 2}, {Index: 3, GainDB: 32.5}})
	if err != nil {
		panic(err)
	}
	return st
}

// micHeadamps describes headamps 1..n with the mic law (-12..+60 dB, 0.5 dB).
func micHeadamps(n int) []model.HeadampInfo {
	out := make([]model.HeadampInfo, n)
	for i := range out {
		out[i] = model.HeadampInfo{Index: i + 1, Bank: model.BankLocal, Number: i + 1, MinDB: -12, MaxDB: 60, WriteMaxDB: 59.5, StepDB: 0.5, Writable: true}
	}
	return out
}

func startSafe(t *testing.T, cfg SafetyConfig, st model.State) (*Supervisor, *fakeDriver) {
	t.Helper()
	drv := &fakeDriver{snapshot: st, caps: gainCaps}
	sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}}, Safety: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Stop)
	synctest.Wait()
	return sup, drv
}

func set(p model.ParamRef, v model.Value, o driver.Origin) Write {
	return Write{Command: driver.Command{Param: p, Value: v, Origin: o}}
}

func dbIn(t *testing.T, r Result) float64 {
	t.Helper()
	db, _ := r.Value.DB()
	return db
}

// lastWrite returns the value of the last write the console got (-1000: none).
func lastWrite(drv *fakeDriver) float64 {
	w := drv.session(len(drv.sessions) - 1).writes()
	if len(w) == 0 {
		return -1000
	}
	db, _ := w[len(w)-1].Value.DB()
	return db
}

// gainState is the safety fixture with headamp 3 at db.
func gainState(db float64) model.State {
	st, err := stateWith(-6).WithHeadamps([]model.Headamp{{Index: 1}, {Index: 2}, {Index: 3, GainDB: db}})
	if err != nil {
		panic(err)
	}
	return st
}

// CC-2, CC-3 (replaces the v0.3 caps of AC-PS-03/04): a pull of the Gain
// wheel to the bottom moves at most 6 dB per write and stops only at the
// headamp's minDB. Each write is echoed, as by a console that echoes.
func TestGain_PullToTheBottomSixPerWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.HoldWindow(gain3, gesture, true)
		for _, tc := range []struct {
			want float64
			st   GestureState
		}{{26.5, StateFree}, {20.5, StateFree}, {14.5, StateFree}, {8.5, StateFree}, {2.5, StateFree}, {-3.5, StateFree}, {-9.5, StateFree}, {-12, StateAtCap}} {
			r, _ := sup.Write(set(gain3, lvl(t, -40), gesture))
			if dbIn(t, r) != tc.want || r.State != tc.st {
				t.Fatalf("pull: %v %s, want %v %s", dbIn(t, r), r.State, tc.want, tc.st)
			}
			drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, tc.want), Origin: gesture}
			synctest.Wait()
		}
	})
}

// AC-PS-19, SR-31: every Gain write path is bounded to 6 dB from the console
// value; only a typed entry may move further, after confirmation.
func TestGain_PerWriteBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		for name, w := range map[string]Write{
			"gesture": set(gain3, lvl(t, 45), gesture), "macro": set(gain3, lvl(t, 45), macro),
			"undo":         {Command: driver.Command{Param: gain3, Value: lvl(t, 45), Origin: gesture}, Undo: true},
			"other device": set(gain3, lvl(t, 45), engineerB), "down": set(gain3, lvl(t, 20), gesture),
		} {
			if _, err := sup.Write(w); !errors.Is(err, ErrGainBound) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes past the bound", n)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); err != nil || lastWrite(drv) != 38.5 {
			t.Fatalf("6.0 dB must pass: %v", err)
		}
		typed := Write{Command: driver.Command{Param: gain3, Value: lvl(t, 45), Origin: gesture}, Typed: true}
		if _, err := sup.Write(typed); !errors.Is(err, ErrNeedsConfirm) {
			t.Fatalf("typed +12.5: %v", err)
		}
		typed.Confirmed = true
		if _, err := sup.Write(typed); err != nil || lastWrite(drv) != 45 {
			t.Fatalf("typed and confirmed: %v", err)
		}
	})
}

// AC-PS-16, SR-30: typed raises over 6 dB ask; lowering and small raises do not.
func TestTyped_BigRaiseNeedsConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct {
			from, to float64
			ask      bool
		}{{-6, -10, false}, {-6, -2, false}, {-20, 0, true}, {math.Inf(-1), -30, true}, {-6, 0, false}} {
			sup, drv := startSafe(t, SafetyConfig{}, safetyState(tc.from))
			w := Write{Command: driver.Command{Param: ch1Fader, Value: lvl(t, tc.to), Origin: gesture}, Typed: true}
			_, err := sup.Write(w)
			if errors.Is(err, ErrNeedsConfirm) != tc.ask || (tc.ask && lastWrite(drv) != -1000) {
				t.Fatalf("%v -> %v: %v", tc.from, tc.to, err)
			}
			sup.Stop()
		}
	})
}

// Review HIGH 1: the Gain bound holds against the value still in flight,
// not only the echoed one. Gain 32.5: a macro writes 26.5, then 38.5 before
// any echo; the second would move the console +12 dB in one write.
func TestGain_BoundAgainstThePendingValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 26.5), macro)); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("38.5 after a pending 26.5: %v", err)
		}
		if lastWrite(drv) != 26.5 {
			t.Fatalf("last write %v", lastWrite(drv))
		}
		time.Sleep(3 * time.Second) // no echo: the value in flight stays the reference (re-review MEDIUM B)
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); !errors.Is(err, ErrGainBound) {
			t.Fatalf("38.5 3 s after an unechoed 26.5: %v", err)
		}
		_, _ = sup.HoldWindow(gain3, gesture, true) // inside a window the pending value clamps too
		if r, _ := sup.Write(set(gain3, lvl(t, 35.5), gesture)); dbIn(t, r) > 32.5 {
			t.Fatalf("drag up with 26.5 pending applied %v, more than 6 dB above it", dbIn(t, r))
		}
		_, _ = sup.HoldWindow(gain3, gesture, false)
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 32.5), Origin: driver.Origin{Kind: driver.OriginConsole}, Authoritative: true}
		synctest.Wait() // the re-read reports 32.5: the 26.5 never landed
		if _, err := sup.Write(set(gain3, lvl(t, 38.5), macro)); err != nil {
			t.Fatalf("after the console's report only its value counts: %v", err)
		}
	})
}

// Re-review MEDIUM B: Gain echo is unverified (X32-F79, XA-F116), so the
// value in flight never expires on a timer. The probe without echo: 38.5,
// 2.1 s, 26.5, 2.1 s, 38.5 sent all three, a +12 dB step at the console.
func TestGain_InFlightBoundHoldsWithoutEcho(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		var errs []error
		for i, db := range []float64{38.5, 26.5, 38.5} {
			if i > 0 {
				time.Sleep(2100 * time.Millisecond)
			}
			_, err := sup.Write(set(gain3, lvl(t, db), macro))
			errs = append(errs, err)
		}
		if errs[0] != nil || !errors.Is(errs[1], ErrGainBound) || errs[2] != nil {
			t.Fatalf("errors %v, want 26.5 refused", errs)
		}
		prev := 32.5
		for _, w := range drv.session(0).writes() {
			db, _ := w.Value.DB()
			if math.Abs(db-prev) > gainBoundDB {
				t.Fatalf("a write moved Gain %v -> %v", prev, db)
			}
			prev = db
		}
	})
}

// Re-review MEDIUM B with echo: the echo ends the value it matches, an
// authoritative re-read ends them all and its value is the reference from
// then on; a tap of the value in flight while the console still reports
// another is sent again, never skipped as a no-op.
func TestGain_ConsoleReportEndsTheInFlightValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		push := func(db float64, o driver.Origin) {
			drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, db), Origin: o}
			synctest.Wait()
		}
		_, _ = sup.Write(set(gain3, lvl(t, 38.5), gesture))
		if r, err := sup.Write(set(gain3, lvl(t, 38.5), gesture)); err != nil || r.Written {
			t.Fatalf("38.5 again at once: written %v, %v; at most once per echo window (LOW 5)", r.Written, err)
		}
		time.Sleep(lateEchoWindowDefault)
		if r, err := sup.Write(set(gain3, lvl(t, 38.5), gesture)); err != nil || !r.Written {
			t.Fatalf("38.5 again an echo window later, before any report: written %v, %v", r.Written, err)
		}
		push(38.5, gesture) // the echo
		if _, err := sup.Write(set(gain3, lvl(t, 44.5), gesture)); err != nil {
			t.Fatalf("+6 from the echoed 38.5: %v", err)
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain3, Value: lvl(t, 38.5), Origin: driver.Origin{Kind: driver.OriginConsole}, Authoritative: true}
		synctest.Wait() // the re-read says 38.5: 44.5 never landed
		if _, err := sup.Write(set(gain3, lvl(t, 32.5), gesture)); err != nil {
			t.Fatalf("-6 from the reported 38.5: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 4 {
			t.Fatalf("%d writes, want 4", n)
		}
	})
}

// Review HIGH 1, show mode: a pending lowering followed by a raise back to
// the console value is a raise of the console.
func TestShowMode_RaiseAfterPendingLowering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 31.5), gesture)); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 32.5), gesture)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("raise back to 32.5 over a pending 31.5: %v", err)
		}
	})
}

// Review MEDIUM 5: only a person types. A macro (or any non-gesture origin)
// claiming typed would skip the Gain bound SR-31 puts on macros.
func TestTyped_OnlyFromAGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		w := set(gain3, lvl(t, 45), macro)
		w.Typed, w.Confirmed = true, true
		if _, err := sup.Write(w); !errors.Is(err, ErrTypedOrigin) {
			t.Fatalf("typed macro: %v", err)
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}

// x32: a patch change brings in an AES50 headamp mid-session. Its first
// value makes the next update a snapshot, so the UI gets the headamp's
// law and label with it (state.schema.json headamps).
func TestHeadamp_NewHeadampMakesTheNextUpdateASnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Drain()
		gain4 := model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: 4}, Field: model.FieldGain}
		drv.session(0).push <- driver.ParamChanged{Param: gain4, Value: lvl(t, 20), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		u := sup.Drain()
		if !u.Full || len(u.State.Headamps()) != 4 {
			t.Fatalf("full %v headamps %+v", u.Full, u.State.Headamps())
		}
		drv.session(0).push <- driver.ParamChanged{Param: gain4, Value: lvl(t, 21), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		if u := sup.Drain(); u.Full || len(u.Changes) != 1 {
			t.Fatalf("a known headamp is a diff: full %v changes %d", u.Full, len(u.Changes))
		}
	})
}

// Re-review MEDIUM A: the value in flight belongs to the session that sent
// it. A write of 33.5 lost with its session, then a reconnect that reads
// 32.5: tapping 33.5 again is sent (it was a no-op with written false).
func TestGain_InFlightValueDroppedOnStaleAndReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); err != nil {
			t.Fatal(err)
		}
		drv.setDown(true)
		drv.session(0).lose() // before the echo
		synctest.Wait()
		drv.setDown(false)
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		r, err := sup.Write(set(gain3, lvl(t, 33.5), gesture))
		if s := drv.session(1); err != nil || !r.Written || s == nil || len(s.writes()) != 1 {
			t.Fatalf("33.5 after the reconnect: written %v, %v", r.Written, err)
		}
	})
}

// Re-review MEDIUM A: a write the session failed to send is not in flight.
func TestGain_FailedApplyIsNotInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		sendErr := errors.New("send failed")
		drv.session(0).failApplies(sendErr)
		if _, err := sup.Write(set(gain3, lvl(t, 33.5), gesture)); !errors.Is(err, sendErr) {
			t.Fatalf("failed send: %v", err)
		}
		drv.session(0).failApplies(nil)
		r, err := sup.Write(set(gain3, lvl(t, 33.5), gesture))
		if err != nil || !r.Written || len(drv.session(0).writes()) != 1 {
			t.Fatalf("33.5 again: written %v, %v", r.Written, err)
		}
	})
}

// Re-review LOW D: the Gain cap of a gesture stops at the headamp's
// writable top (+59.5 dB), never at the wire maximum the driver refuses.
func TestGain_CapStopsAtTheWritableTop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, _ := stateWith(-6).WithHeadamps([]model.Headamp{{Index: 1}, {Index: 2}, {Index: 3, GainDB: 58}})
		sup, drv := startSafe(t, SafetyConfig{}, st)
		w, _ := sup.HoldWindow(gain3, gesture, true)
		r, _ := sup.Write(set(gain3, lvl(t, 61), gesture))
		if w.Window.CapUp != 59.5 || dbIn(t, r) != 59.5 || r.State != StateAtCap || lastWrite(drv) != 59.5 {
			t.Fatalf("cap %v, applied %v %s", w.Window.CapUp, dbIn(t, r), r.State)
		}
	})
}
