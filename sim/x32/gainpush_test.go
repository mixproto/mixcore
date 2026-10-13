// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// NoGainPush ([UNVERIFIED] variant of X32-F79): a client's Gain write is
// applied and read back but pushed to nobody; a surface change still is;
// other parameters are pushed as before.
func TestSim_NoGainPushVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{NoGainPush: true})
		a, b := h.client("10.0.0.2"), h.client("10.0.0.3")
		b.send("/xremote")
		synctest.Wait()
		a.send("/headamp/002/gain", osc.Float(0.5))
		if m, ok := b.recv(100 * time.Millisecond); ok {
			t.Fatalf("a Gain write was pushed: %s", m.String())
		}
		if v, _ := h.sim.Value("/headamp/002/gain"); v.Float != 0.5 {
			t.Fatalf("Gain not applied: %v", v.Float)
		}
		a.send("/headamp/002/gain")
		if m, ok := a.recv(time.Second); !ok || m.String() != "/headamp/002/gain ,f 0.5" {
			t.Fatalf("read back %s %v", m.String(), ok)
		}
		a.send("/ch/01/mix/on", osc.Int(0))
		if m, ok := b.recv(time.Second); !ok || m.String() != "/ch/01/mix/on ,i 0" {
			t.Fatalf("other writes are still pushed: %s %v", m.String(), ok)
		}
		if err := h.sim.SurfaceSet("/headamp/002/gain", osc.Float(0.25)); err != nil {
			t.Fatal(err)
		}
		if m, ok := b.recv(time.Second); !ok || m.String() != "/headamp/002/gain ,f 0.25" {
			t.Fatalf("a surface Gain change is pushed: %s %v", m.String(), ok)
		}
	})
}
