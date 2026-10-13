// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// NoGainPush ([TO-VERIFY] XA-F116, item 22): a client's Gain write is
// applied and read back but pushed to nobody, not even the sender that
// holds /xremote (the default XA-F23 pushes to it); a surface change and
// other parameters are pushed as before.
func TestSim_NoGainPushVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{NoGainPush: true})
		a := h.client("10.0.0.2")
		a.send("/xremote")
		synctest.Wait()
		a.send("/headamp/02/gain", osc.Float(0.5))
		if m, ok := a.recv(100 * time.Millisecond); ok {
			t.Fatalf("a Gain write was pushed: %s", m.addr)
		}
		if v, ok := a.read("/headamp/02/gain"); !ok || v.Float != 0.5 {
			t.Fatalf("read back %v %v", v, ok)
		}
		a.send("/ch/01/mix/on", osc.Int(0))
		if m, ok := a.recv(time.Second); !ok || m.addr != "/ch/01/mix/on" {
			t.Fatalf("other writes are still pushed: %+v %v", m, ok)
		}
		if err := h.sim.SurfaceSet("/headamp/02/gain", osc.Float(0.25)); err != nil {
			t.Fatal(err)
		}
		if m, ok := a.recv(time.Second); !ok || m.addr != "/headamp/02/gain" || m.arg.Float != 0.25 {
			t.Fatalf("a surface Gain change is pushed: %+v %v", m, ok)
		}
	})
}
