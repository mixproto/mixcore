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

func TestReconnect_ShortLivedSessionsStillBackOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0), flaky: true}
		start(t, drv)
		time.Sleep(10 * time.Second)
		at := drv.attempts()
		if len(at) > 12 {
			t.Fatalf("%d connects in 10 s: a console that drops us at once must get backoff", len(at))
		}
		if got := at[2].Sub(at[1]); got != backoff.Default.Delay(2, 0.5) {
			t.Fatalf("delay before attempt 3 = %v, want %v", got, backoff.Default.Delay(2, 0.5))
		}
	})
}

func TestHold_BeginRejectedWhileNotLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0), down: true}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Hold(ch1Fader, gesture, true); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("holdBegin while connecting = %v, want ErrStale", err)
		}
		if err := sup.Hold(ch1Fader, gesture, false); err != nil {
			t.Fatalf("holdEnd must always work: %v", err)
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

func TestHeldFader_ExternalChangeEndsGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); err != nil {
			t.Fatal(err)
		}
		// Our own echo does not end the gesture.
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}
		synctest.Wait()
		if u := sup.Drain(); len(u.Events) != 0 || faderOf(t, u) != -3 {
			t.Fatalf("own echo: events %+v fader %v", u.Events, faderOf(t, u))
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -4), Origin: gesture}); err != nil {
			t.Fatalf("gesture must continue after its own echo: %v", err)
		}

		// The engineer moves the fader at the desk.
		desk := driver.Origin{Kind: driver.OriginConsole}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -10), Origin: desk}
		synctest.Wait()
		u := sup.Drain()
		if faderOf(t, u) != -10 {
			t.Fatalf("the console's value must be published at once, got %v", faderOf(t, u))
		}
		if len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange || u.Events[0].ClientID != "ui" ||
			!u.Events[0].Value.Equal(lvl(t, -10)) || u.Events[0].Origin != desk {
			t.Fatalf("events = %+v, want one external change on the held fader", u.Events)
		}
		writesBefore := len(drv.session(0).writes())
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -5), Origin: gesture}); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("Apply after an external change = %v, want ErrGestureEnded", err)
		}
		if n := len(drv.session(0).writes()); n != writesBefore {
			t.Fatal("an ended gesture reached the console")
		}
		// Other parameters and other clients are not affected.
		mute := model.ParamRef{Strip: ch1Fader.Strip, Field: model.FieldMute}
		if err := sup.Apply(driver.Command{Param: mute, Value: model.Bool(true), Origin: gesture}); err != nil {
			t.Fatalf("mute of the same client: %v", err)
		}
		tablet := driver.Origin{Kind: driver.OriginGesture, ClientID: "tablet"}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -5), Origin: tablet}); err != nil {
			t.Fatalf("another client: %v", err)
		}
		// A new hold-begin starts a new gesture.
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -5), Origin: gesture}); err != nil {
			t.Fatalf("after a new hold-begin: %v", err)
		}
		if err := sup.Hold(mute, gesture, true); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("hold on a mute err = %v", err)
		}
	})
}

func TestHeldFader_EndedGestureClearedByRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		_ = sup.Hold(ch1Fader, gesture, true)
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -10), Origin: driver.Origin{Kind: driver.OriginConsole}}
		synctest.Wait()
		_ = sup.Hold(ch1Fader, gesture, false) // finger lifted
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -2), Origin: gesture}); err != nil {
			t.Fatalf("a tap after the finger was lifted must work: %v", err)
		}
	})
}

func TestHeldFader_OtherClientsWriteEndsGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		_ = sup.Hold(ch1Fader, gesture, true)
		tablet := driver.Origin{Kind: driver.OriginGesture, ClientID: "tablet", Seq: 1}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -8), Origin: tablet} // echo of the tablet's write
		synctest.Wait()
		if u := sup.Drain(); len(u.Events) != 1 || u.Events[0].ClientID != "ui" {
			t.Fatalf("another client's write must end ui's gesture: %+v", u.Events)
		}
	})
}

func TestHeldFader_SceneRecallEndsGesture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		_ = sup.Hold(ch1Fader, gesture, true)
		drv.session(0).push <- driver.StateReplaced{State: stateWith(-20), Reason: "scene recall"}
		synctest.Wait()
		u := sup.Drain()
		if !u.Full || faderOf(t, u) != -20 || len(u.Events) != 1 || u.Events[0].Kind != HoldExternalChange {
			t.Fatalf("after recall: full %v fader %v events %+v", u.Full, faderOf(t, u), u.Events)
		}
	})
}

func TestStale_CancelsActiveHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		sup.Drain()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		drv.setDown(true)
		drv.session(0).lose()
		synctest.Wait()
		u := sup.Drain()
		if u.Status != StatusStale || len(u.Events) != 1 || u.Events[0].Kind != HoldCancelled || u.Events[0].Reason != "stale" {
			t.Fatalf("after loss: status %v events %+v", u.Status, u.Events)
		}
		drv.setDown(false)
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		if u := sup.Drain(); u.Status != StatusLive {
			t.Fatalf("did not reconnect: %v", u.Status)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("Apply on the cancelled hold after Live = %v, want ErrGestureEnded", err)
		}
		if n := len(drv.session(1).writes()); n != 0 {
			t.Fatalf("%d writes resumed without a new hold-begin", n)
		}
		_ = sup.Hold(ch1Fader, gesture, true)
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); err != nil {
			t.Fatalf("new hold after reconnect: %v", err)
		}
		_ = sup.Hold(ch1Fader, gesture, true)
		sup.Stop()
		if u := sup.Drain(); len(u.Events) != 1 || u.Events[0].Reason != "stopped" {
			t.Fatalf("Stop must cancel holds: %+v", u.Events)
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
