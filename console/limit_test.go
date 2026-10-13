// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/backoff"
	"github.com/mixproto/mixcore/model"
)

// SR-17: while the console does not send us updates, values may be stale:
// input is locked and holds end, although the connection is up.
func TestLimit_LocksInputCancelsHoldsAndClears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		sup.Drain()
		drv.session(0).push <- driver.ClientLimitHit{Limit: 4, Reason: driver.LimitNoEcho}
		synctest.Wait()
		u := sup.Drain()
		if u.Status != StatusLive || !u.Limited || u.LimitedReason != driver.LimitNoEcho || !u.Full {
			t.Fatalf("after limit: %+v", u)
		}
		if len(u.Events) != 1 || u.Events[0].Kind != HoldCancelled || u.Events[0].Reason != ReasonLimited {
			t.Fatalf("events = %+v, want the hold cancelled (limited)", u.Events)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("Apply while limited = %v, want ErrStale", err)
		}
		if err := sup.Hold(ch1Fader, gesture, true); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("hold while limited = %v, want ErrStale", err)
		}
		drv.session(0).push <- driver.ClientLimitCleared{}
		synctest.Wait()
		if u := sup.Drain(); u.Limited || u.LimitedReason != "" {
			t.Fatalf("after clear: %+v", u)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("Apply of the cancelled gesture after clear = %v, want ErrGestureEnded", err)
		}
		if err := sup.Hold(ch1Fader, gesture, false); err != nil { // the finger lifts
			t.Fatal(err)
		}
		if err := sup.Apply(driver.Command{Param: ch1Fader, Value: lvl(t, -3), Origin: gesture}); err != nil {
			t.Fatalf("Apply after clear and lift: %v", err)
		}
	})
}

// A reconnect starts unlimited: the new session gathers its own evidence.
func TestLimit_ClearedByReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		drv.session(0).push <- driver.ClientLimitHit{Limit: 4, Reason: driver.LimitUnpushedChanges}
		synctest.Wait()
		drv.session(0).lose()
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		if u := sup.Drain(); u.Status != StatusLive || u.Limited {
			t.Fatalf("after reconnect: %+v", u)
		}
	})
}

// Review go M1: a session whose console went silent locks input (stale,
// holds cancelled, writes refused) while it keeps the connection; when the
// console answers again, input unlocks without a reconnect.
func TestConsoleSilent_LocksAndRecoversWithoutReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWithGroups()}
		sup := start(t, drv)
		synctest.Wait()
		fader := model.ParamRef{Strip: ch1, Field: model.FieldFader}
		if err := sup.Hold(fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		sup.Drain()
		drv.session(0).push <- driver.ConsoleSilent{Since: time.Now()}
		synctest.Wait()
		u := sup.Drain()
		if u.Status != StatusStale || len(u.Events) != 1 || u.Events[0].Kind != HoldCancelled {
			t.Fatalf("silent: status %s, events %+v", u.Status, u.Events)
		}
		if err := sup.Apply(driver.Command{Param: fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("write while silent: %v", err)
		}
		drv.session(0).push <- driver.ConsoleResponding{}
		synctest.Wait()
		if u := sup.Drain(); u.Status != StatusLive {
			t.Fatalf("responding: status %s", u.Status)
		}
		if err := sup.Apply(driver.Command{Param: fader, Value: lvl(t, -3), Origin: gesture}); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("the cancelled hold must be grabbed again: %v", err)
		}
		if err := sup.Hold(fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		if err := sup.Apply(driver.Command{Param: fader, Value: lvl(t, -3), Origin: gesture}); err != nil {
			t.Fatalf("write after recovery: %v", err)
		}
		drv.mu.Lock()
		n := len(drv.connectAt)
		drv.mu.Unlock()
		if n != 1 {
			t.Fatalf("%d connects: silence must not reconnect by itself", n)
		}
	})
}
