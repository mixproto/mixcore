// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/backoff"
)

var errBroken = errors.New("fake: socket failed")

// fakeLinkDriver adds driver.LinkOpener to fakeDriver: its links hand out the
// fakeDriver's sessions, so a test sees both links and sessions.
type fakeLinkDriver struct {
	*fakeDriver
	lmu   sync.Mutex
	links []*fakeLink
}

type fakeLink struct {
	d      *fakeDriver
	mu     sync.Mutex
	err    error
	closed bool
}

func (d *fakeLinkDriver) OpenLink(context.Context, driver.Endpoint, driver.Options) (driver.Link, error) {
	d.lmu.Lock()
	defer d.lmu.Unlock()
	l := &fakeLink{d: d.fakeDriver}
	d.links = append(d.links, l)
	return l, nil
}

func (d *fakeLinkDriver) link(i int) *fakeLink {
	d.lmu.Lock()
	defer d.lmu.Unlock()
	if i >= len(d.links) {
		return nil
	}
	return d.links[i]
}

func (d *fakeLinkDriver) linkCount() int {
	d.lmu.Lock()
	defer d.lmu.Unlock()
	return len(d.links)
}

func (l *fakeLink) Connect(ctx context.Context) (driver.Session, error) {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return nil, driver.ErrClosed
	}
	return l.d.Connect(ctx, driver.Endpoint{}, driver.Options{})
}

func (l *fakeLink) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *fakeLink) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return nil
}

func (l *fakeLink) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (l *fakeLink) breakIt() {
	l.mu.Lock()
	l.err = errBroken
	l.mu.Unlock()
}

func startLink(t *testing.T, drv *fakeLinkDriver) *Supervisor {
	t.Helper()
	sup, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }})
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Stop)
	return sup
}

// One link serves every reconnect, so a console that counts source ports sees
// one client however often the connection drops (X32-F28).
func TestLink_OpenedOnceAndReusedAcrossReconnects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeLinkDriver{fakeDriver: &fakeDriver{snapshot: stateWith(0)}}
		sup := startLink(t, drv)
		synctest.Wait()
		for i := range 20 {
			drv.session(i).lose()
			time.Sleep(backoff.Default.Max)
			synctest.Wait()
		}
		if drv.session(20) == nil || sup.Drain().Status != StatusLive {
			t.Fatal("did not reconnect 20 times")
		}
		if n := drv.linkCount(); n != 1 {
			t.Fatalf("%d links opened, want 1 for 20 reconnects", n)
		}
	})
}

func TestLink_ReopenedOnlyAfterItBroke(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeLinkDriver{fakeDriver: &fakeDriver{snapshot: stateWith(0)}}
		startLink(t, drv)
		synctest.Wait()
		drv.link(0).breakIt()
		drv.session(0).lose()
		time.Sleep(backoff.Default.Max)
		synctest.Wait()
		if n := drv.linkCount(); n != 2 || !drv.link(0).isClosed() {
			t.Fatalf("%d links, first closed %v; want a new link and the broken one closed", n, drv.link(0).isClosed())
		}
	})
}

// Pause keeps the link (a quick resume keeps the console slot); Stop closes it.
func TestLink_KeptByPauseClosedByStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		drv := &fakeLinkDriver{fakeDriver: &fakeDriver{snapshot: stateWith(0)}}
		sup := startLink(t, drv)
		synctest.Wait()
		sup.Pause()
		if u := sup.Drain(); u.Status != StatusStopped || drv.link(0).isClosed() {
			t.Fatalf("after Pause: status %v, link closed %v", u.Status, drv.link(0).isClosed())
		}
		if err := sup.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if n := drv.linkCount(); n != 1 || sup.Drain().Status != StatusLive {
			t.Fatalf("after resume: %d links, want the same one, live", n)
		}
		sup.Stop()
		if !drv.link(0).isClosed() {
			t.Fatal("Stop must close the link")
		}
	})
}
