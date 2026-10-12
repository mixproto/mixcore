// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func (r *rig) openLink() driver.Link {
	r.t.Helper()
	l, err := r.drv.OpenLink(context.Background(), driver.Endpoint{Host: consoleIP}, r.opts())
	if err != nil {
		r.t.Fatalf("OpenLink: %v", err)
	}
	r.t.Cleanup(func() { _ = l.Close() })
	return l
}

// sourcePorts returns the distinct client ports the simulator received any
// packet from, and those that sent /xremote.
func sourcePorts(sim *simx32.Sim) (all, xremote map[netip.AddrPort]bool) {
	all, xremote = map[netip.AddrPort]bool{}, map[netip.AddrPort]bool{}
	for _, e := range sim.Log() {
		all[e.From] = true
		if e.Address == "/xremote" {
			xremote[e.From] = true
		}
	}
	return all, xremote
}

// X32-F28: reconnects over one link keep both source ports, so the console
// sees one /xremote client however often we reconnect.
func TestLink_ReconnectsKeepTheSameSourcePorts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		l := r.openLink()
		for range 5 {
			sess, err := l.Connect(context.Background())
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			_, stop := r.run(sess)
			r.sim.Pause()
			time.Sleep(DefaultTiming.DeadAfter + DefaultTiming.PingEvery)
			if err := stop(); !errors.Is(err, driver.ErrConnectionLost) {
				t.Fatalf("Run err = %v, want ErrConnectionLost", err)
			}
			_ = sess.Close()
			r.sim.Resume()
		}
		all, xremote := sourcePorts(r.sim)
		if len(all) > 2 || len(xremote) != 1 {
			t.Fatalf("source ports: %d in total, %d sent /xremote; want at most 2 and exactly 1", len(all), len(xremote))
		}
		if n := r.sim.MaxSlotsUsed(); n != 1 {
			t.Fatalf("max client slots used = %d, want 1", n)
		}
		if l.Err() != nil {
			t.Fatalf("link reports %v", l.Err())
		}
	})
}

// A new Connect closes the session still open on the link; closing the link
// closes its session; a closed link refuses to connect.
func TestLink_OneSessionAtATimeAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		l := r.openLink()
		first, err := l.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		second, err := l.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := first.Apply(driver.Command{Param: ref(1, 0), Origin: gesture}); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("first session after a new Connect: Apply err = %v, want ErrClosed", err)
		}
		_ = l.Close()
		_ = l.Close() // idempotent
		if err := second.Run(context.Background(), make(chan driver.Event)); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("Run after link Close err = %v", err)
		}
		if _, err := l.Connect(context.Background()); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("Connect on a closed link err = %v", err)
		}
		if _, err := r.drv.OpenLink(context.Background(), driver.Endpoint{Host: "console.local"}, r.opts()); !errors.Is(err, ErrBadEndpoint) {
			t.Fatalf("hostname endpoint err = %v", err)
		}
	})
}

// Closing a link session stops its meters at once, although the sockets stay
// open and the console keeps streaming for up to 10 s (X32-F50).
func TestLink_SessionCloseStopsMeters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.sim.SetInputLevels([]float32{1})
		l := r.openLink()
		sess, err := l.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, stop := r.run(sess)
		_ = sess.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(time.Second)
		_ = stop()
		_ = sess.Close()
		r.slot.Reset()
		time.Sleep(2 * time.Second)
		var fr meters.Frame
		if r.slot.CopyLatest(&fr) {
			t.Fatalf("meters published after the session closed: seq %d", fr.Seq)
		}
	})
}
