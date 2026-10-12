// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/backoff"
)

// Design 8.1: the stale time is set once when the link is lost and stays put
// while reconnects fail; 30 s later the state says offline; live clears both.
func TestStale_SinceStaysPutAndOfflineAfterThirtySeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		if u := sup.Drain(); !u.StaleSince.IsZero() || u.Offline {
			t.Fatalf("live: staleSince %v offline %v", u.StaleSince, u.Offline)
		}
		drv.setDown(true)
		lost := time.Now()
		drv.session(0).lose()
		synctest.Wait()
		u := sup.Drain()
		if u.Status != StatusStale || !u.StaleSince.Equal(lost) || u.Offline {
			t.Fatalf("after loss: %v since %v offline %v; want stale since %v", u.Status, u.StaleSince, u.Offline, lost)
		}
		time.Sleep(OfflineAfter - time.Second)
		synctest.Wait()
		if u := sup.Drain(); u.Offline || !u.StaleSince.Equal(lost) {
			t.Fatalf("29 s: offline %v since %v; failed reconnects must not move it", u.Offline, u.StaleSince)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if u := sup.Drain(); !u.Offline || !u.Full {
			t.Fatalf("30 s: offline %v full %v; want offline in a full update", u.Offline, u.Full)
		}
		drv.setDown(false)
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		if u := sup.Drain(); u.Status != StatusLive || !u.StaleSince.IsZero() || u.Offline {
			t.Fatalf("back live: %v since %v offline %v", u.Status, u.StaleSince, u.Offline)
		}
	})
}

// A short outage never reaches offline, and Stop clears the timer.
func TestStale_ShortOutageNeverOffline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeDriver{snapshot: stateWith(0)}
		sup := start(t, drv)
		synctest.Wait()
		drv.session(0).lose()
		time.Sleep(OfflineAfter * 2)
		synctest.Wait()
		if u := sup.Drain(); u.Offline || u.Status != StatusLive {
			t.Fatalf("status %v offline %v after a reconnect", u.Status, u.Offline)
		}
		drv.setDown(true)
		drv.session(1).lose()
		synctest.Wait()
		sup.Stop()
		time.Sleep(OfflineAfter * 2)
		if u := sup.Drain(); u.Offline || u.Status != StatusStopped || !u.StaleSince.IsZero() {
			t.Fatalf("after Stop: %v offline %v since %v", u.Status, u.Offline, u.StaleSince)
		}
	})
}
