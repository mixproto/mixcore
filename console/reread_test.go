// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
)

func TestReread_EveryIntervalWhileLiveAndNeverWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		start(t, drv)
		synctest.Wait()
		time.Sleep(time.Hour + time.Second)
		synctest.Wait()
		sess := drv.session(0)
		if got, want := sess.rereadCount(), int(time.Hour/DefaultRereadEvery); got != want {
			t.Fatalf("%d re-reads in an hour, want %d (every %s)", got, want, DefaultRereadEvery)
		}
		if n := len(sess.writes()); n != 0 {
			t.Fatalf("background re-read produced %d writes", n) // safety rule 2
		}
	})
}

// A session that cannot leave parameters out keeps the old rule.
func TestReread_SkippedWhileAHoldIsActive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * DefaultRereadEvery)
		synctest.Wait()
		if n := drv.session(0).rereadCount(); n != 0 {
			t.Fatalf("%d re-reads while a fader was held", n)
		}
		if err := sup.Hold(ch1Fader, gesture, false); err != nil {
			t.Fatal(err)
		}
		time.Sleep(DefaultRereadEvery)
		synctest.Wait()
		if n := drv.session(0).rereadCount(); n != 1 {
			t.Fatalf("%d re-reads after the finger was lifted, want 1", n)
		}
	})
}

// A resting finger (holdBegin from touch-down) must not stop the recovery
// of every other parameter: a selective session re-reads all but the held
// parameters (Apple review, S2c).
func TestReread_SkipsOnlyTheHeldParameters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0), selective: true}
		sup := start(t, drv)
		synctest.Wait()
		if err := sup.Hold(ch1Fader, gesture, true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * DefaultRereadEvery)
		synctest.Wait()
		sess := drv.session(0)
		sess.mu.Lock()
		skips := sess.skips
		sess.mu.Unlock()
		if len(skips) != 3 || len(skips[0]) != 1 || skips[0][0] != ch1Fader {
			t.Fatalf("re-reads while held: %v", skips)
		}
		if n := len(sess.writes()); n != 0 {
			t.Fatalf("%d writes", n)
		}
	})
}

func TestReread_NotWhileStaleAndOffWhenNegative(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}},
			RereadEvery: -1})
		if err != nil {
			t.Fatal(err)
		}
		if err := sup.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer sup.Stop()
		time.Sleep(10 * DefaultRereadEvery)
		synctest.Wait()
		if n := drv.session(0).rereadCount(); n != 0 {
			t.Fatalf("%d re-reads with RereadEvery < 0", n)
		}

		sess := &fakeSession{}
		sup.mu.Lock()
		sup.status = StatusStale
		sup.mu.Unlock()
		sup.reread(sess)
		if sess.rereadCount() != 0 {
			t.Fatal("a re-read must not be requested while stale")
		}
	})
}
