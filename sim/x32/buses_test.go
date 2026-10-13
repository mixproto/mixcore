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

// X32-F80..F89: the bus, send, link, matrix, DCA master and colour
// addresses answer reads with their documented types; pan and tap exist on
// odd sends only (X32-F84, X32-F85), so an even send's pan is unknown.
func TestSim_BusAndSendAddresses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		for addr, want := range map[string]string{
			"/bus/16/mix/fader": "/bus/16/mix/fader ,f 0", "/bus/01/mix/on": "/bus/01/mix/on ,i 1",
			"/bus/02/config/color": "/bus/02/config/color ,i 0", "/config/buslink/15-16": "/config/buslink/15-16 ,i 0",
			"/ch/32/mix/16/level": "/ch/32/mix/16/level ,f 0", "/ch/01/mix/02/on": "/ch/01/mix/02/on ,i 1",
			"/ch/01/mix/01/pan": "/ch/01/mix/01/pan ,f 0.5", "/ch/01/mix/15/type": "/ch/01/mix/15/type ,i 0",
			"/mtx/06/mix/on": "/mtx/06/mix/on ,i 1", "/dca/8/fader": "/dca/8/fader ,f 0", "/main/st/config/color": "/main/st/config/color ,i 0",
		} {
			c.send(addr)
			if m, ok := c.recv(time.Second); !ok || m.String() != want {
				t.Fatalf("read %s = %q, %v; want %q", addr, m.String(), ok, want)
			}
		}
		for _, addr := range []string{"/ch/01/mix/02/pan", "/ch/01/mix/02/type", "/bus/17/mix/fader", "/ch/01/mix/17/level"} {
			c.send(addr)
			if m, ok := c.recv(time.Second); ok {
				t.Fatalf("%s must not answer, got %s", addr, m.String())
			}
		}
	})
}

// X32-F83: a send level is kept on the 161-step grid and pushed to the
// other subscribers like any parameter (X32-F22, X32-F90); a tap outside
// 0..5 and a colour outside 0..15 are refused (logged, not applied).
func TestSim_SendWriteQuantizedAndPushed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		a, b := h.client("10.0.0.2"), h.client("10.0.0.3")
		b.send("/xremote")
		synctest.Wait()
		a.send("/ch/04/mix/02/level", osc.Float(0.7531)) // x32.md item 26: 0.75 if the console keeps 161 steps
		if m, ok := b.recv(time.Second); !ok || m.String() != "/ch/04/mix/02/level ,f 0.75" {
			t.Fatalf("push = %q, %v", m.String(), ok)
		}
		a.send("/ch/04/mix/01/type", osc.Int(6))
		a.send("/bus/02/config/color", osc.Int(16))
		synctest.Wait()
		w := h.sim.Writes()
		if len(w) != 3 || !w[0].Accepted || w[1].Accepted || w[2].Accepted {
			t.Fatalf("write log %+v", w)
		}
	})
}
