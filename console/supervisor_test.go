// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/backoff"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var (
	ch1Fader = model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldFader}
	gesture  = driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 7}
)

func lvl(t *testing.T, db float64) model.Value {
	t.Helper()
	v, err := model.Level(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// start runs a supervisor over drv with a fixed jitter source.
func start(t *testing.T, drv *fakeDriver) *Supervisor {
	t.Helper()
	sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}},
		Meters: driver.MeterRequest{Inputs: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Stop)
	return sup
}

func faderOf(t *testing.T, u Update) float64 {
	t.Helper()
	v, _ := u.State.Value(ch1Fader)
	db, _ := v.DB()
	return db
}

func TestConnect_AdoptsConsoleStateAndGoesLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(-12)}
		sup := start(t, drv)
		synctest.Wait()
		u := sup.Drain()
		if u.Status != StatusLive || !u.Full || faderOf(t, u) != -12 {
			t.Fatalf("update = %+v", u)
		}
		if got := drv.session(0).meters; !got.Inputs {
			t.Fatal("meter request must be applied after connect")
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("connect produced %d writes", n) // rule 1
		}
	})
}

func TestApply_RejectsNonUserOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		for _, k := range []driver.OriginKind{driver.OriginConsole, driver.OriginSystem, 0} {
			err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: driver.Origin{Kind: k}})
			if !errors.Is(err, driver.ErrNotUserOrigin) {
				t.Fatalf("origin %v: err = %v", k, err)
			}
		}
		if err := sup.Hold(ch1Fader, driver.Origin{Kind: driver.OriginSystem}, true); !errors.Is(err, driver.ErrNotUserOrigin) {
			t.Fatalf("hold with system origin err = %v", err)
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("%d writes reached the session", n) // rule 2
		}
	})
}

func TestStale_ApplyReturnsErrStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0), down: true}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("before first connect: err = %v, want ErrStale", err)
		}
		drv.setDown(false)
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		drv.setDown(true)
		drv.session(0).lose()
		synctest.Wait()
		u := sup.Drain()
		if u.Status != StatusStale || faderOf(t, u) != 0 {
			t.Fatalf("after loss: status %v fader %v, want stale with last value kept", u.Status, faderOf(t, u))
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("after loss: err = %v, want ErrStale", err)
		}
	})
}

func TestReconnect_DropsQueuedWritesAndReplacesStateWithConsole(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -6), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		drv.setDown(true)
		drv.session(0).lose()
		synctest.Wait()
		for range 5 { // gestures during the outage are refused, so there is nothing to replay
			if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -20), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
				t.Fatalf("err during outage = %v", err)
			}
		}
		drv.setSnapshot(stateWith(-40)) // an engineer moved the fader at the desk meanwhile
		drv.setDown(false)
		time.Sleep(backoff.Default.Max)
		synctest.Wait()

		second := drv.session(1)
		if second == nil {
			t.Fatal("did not reconnect")
		}
		if w := second.writes(); len(w) != 0 {
			t.Fatalf("writes replayed after reconnect: %+v", w) // rule 3
		}
		u := sup.Drain()
		if u.Status != StatusLive || !u.Full || faderOf(t, u) != -40 {
			t.Fatalf("after reconnect: status %v full %v fader %v, want live with the console's -40", u.Status, u.Full, faderOf(t, u))
		}
	})
}

func TestReconnect_BackoffWithJitterAndNeverGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{down: true}
		start(t, drv)
		time.Sleep(time.Minute)
		at := drv.attempts()
		if len(at) < 20 {
			t.Fatalf("only %d attempts in a minute; the supervisor must keep trying", len(at))
		}
		p := backoff.Default
		for i := 1; i < 6; i++ {
			want := p.Delay(i, 0.5)
			if got := at[i].Sub(at[i-1]); got != want {
				t.Fatalf("delay before attempt %d = %v, want %v", i+1, got, want)
			}
		}
		for i := 6; i < len(at); i++ {
			if got := at[i].Sub(at[i-1]); got > p.Max {
				t.Fatalf("delay %v exceeds the %v cap", got, p.Max)
			}
		}
	})
}

func TestEcho_OwnWriteDoesNotBounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -6), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -6), Origin: gesture} // the echo
		synctest.Wait()
		u := sup.Drain()
		if len(u.Changes) != 1 || u.Changes[0].Origin != gesture || faderOf(t, u) != -6 {
			t.Fatalf("update = %+v", u)
		}
		time.Sleep(time.Minute)
		if n := len(drv.session(0).writes()); n != 1 {
			t.Fatalf("session got %d writes, want only the user's one", n)
		}
	})
}

func TestHeldFader_ConsoleUpdateAppliedOnRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		console := driver.Origin{Kind: driver.OriginConsole}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -30), Origin: console}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -25), Origin: console}
		synctest.Wait()
		if u := sup.Drain(); faderOf(t, u) != 0 || len(u.Changes) != 0 {
			t.Fatalf("while held the fader must not move: %v, %d changes", faderOf(t, u), len(u.Changes))
		}
		if err := sup.Hold(ch1Fader, gesture, false); err != nil {
			t.Fatal(err)
		}
		u := sup.Drain()
		if faderOf(t, u) != -25 || len(u.Changes) != 1 {
			t.Fatalf("on release the latest console value must apply: %v, %+v", faderOf(t, u), u.Changes)
		}
		if n := len(drv.session(0).writes()); n != 0 {
			t.Fatalf("holding produced %d writes", n)
		}
		mute := model.ParamRef{Strip: ch1Fader.Strip, Field: model.FieldMute}
		if err := sup.Hold(mute, gesture, true); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("hold on a mute err = %v", err)
		}
	})
}

func TestHeldFader_TwoClientsReleaseOnLast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		other := driver.Origin{Kind: driver.OriginGesture, ClientID: "tablet"}
		_ = sup.Hold(ch1Fader, gesture, true)
		_ = sup.Hold(ch1Fader, other, true)
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -10), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		_ = sup.Hold(ch1Fader, gesture, false)
		if faderOf(t, sup.Snapshot()) != 0 {
			t.Fatal("still held by the tablet; must not apply yet")
		}
		_ = sup.Hold(ch1Fader, other, false)
		if faderOf(t, sup.Snapshot()) != -10 {
			t.Fatal("last release must apply the console value")
		}
	})
}

func TestDrain_OverflowGivesFullSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		console := driver.Origin{Kind: driver.OriginConsole}
		for i := range maxPendingChanges + 10 {
			drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -float64(i%60)), Origin: console}
		}
		synctest.Wait()
		if u := sup.Drain(); !u.Full || u.Changes != nil {
			t.Fatalf("overflow must turn into one full snapshot, got full=%v changes=%d", u.Full, len(u.Changes))
		}
	})
}

func TestStopAndRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(-5)}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Start(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("second Start err = %v", err)
		}
		sup.Stop()
		sup.Stop() // idempotent
		if u := sup.Drain(); u.Status != StatusStopped || faderOf(t, u) != -5 {
			t.Fatalf("after Stop: %+v", u)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, 0), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("Apply after Stop err = %v", err)
		}
		if err := sup.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if u := sup.Drain(); u.Status != StatusLive {
			t.Fatalf("restart status = %v", u.Status)
		}
		if n := len(drv.session(1).writes()); n != 0 {
			t.Fatalf("restart wrote %d times", n)
		}
	})
}

func TestNew_Validates(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("nil driver must be rejected")
	}
	if _, err := New(Config{Driver: &fakeDriver{}, Policy: backoff.Policy{Initial: -1}}); !errors.Is(err, backoff.ErrInvalidPolicy) {
		t.Fatalf("bad policy err = %v", err)
	}
	if StatusStale.String() != "stale" || Status(9).String() != "unknown" {
		t.Fatal("Status.String mismatch")
	}
}
