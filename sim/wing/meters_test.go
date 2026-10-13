// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/memnet"
)

// TestMeters_VendorRequestAndLifetime: the vendor sequence of WG-F51
// (port 0x3737, report id 2, channel 2), 50 ms packets of 4 + 16 bytes,
// gone 5 s after the last D4 (WG-F51, WG-F52).
func TestMeters_VendorRequestAndLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		r := newRig(t, Options{})
		pc, _ := n.ListenPacket("10.0.0.1:2222")
		r.wg.Go(func() { _ = r.sim.ServeUDP(r.ctx, pc) })
		t.Cleanup(func() { _ = pc.Close() })
		rx, _ := n.ListenPacket("10.0.0.2:14135") // 0x3737
		defer rx.Close()
		r.sim.SetInputLevel(2, -6)
		p := r.dial()
		p.send("DFD3 D33737 DFD1")
		p.send("DFD3 D400000002 DCA001DE DFD1")
		buf := make([]byte, 2048)
		_ = rx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		k, _, err := rx.ReadFrom(buf)
		if err != nil || k != 4+2*8 || binary.BigEndian.Uint32(buf) != 2 || int16(binary.BigEndian.Uint16(buf[4:])) != -6*256 {
			t.Fatalf("meter packet: %d bytes % X %v", k, buf[:k], err)
		}
		time.Sleep(6 * time.Second)
		for { // drain what was sent before the lifetime ended
			_ = rx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if _, _, err := rx.ReadFrom(buf); err != nil {
				break
			}
		}
		if _, _, err := rx.ReadFrom(buf); err == nil {
			t.Fatal("meters still sent 5 s after the last renewal")
		}
	})
}

func TestDiscovery_Reply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		r := newRig(t, Options{Name: "PGM", Model: "ngc-full", IP: "10.0.0.1"})
		pc, _ := n.ListenPacket("10.0.0.1:2222")
		r.wg.Go(func() { _ = r.sim.ServeUDP(r.ctx, pc) })
		t.Cleanup(func() { _ = pc.Close() })
		c, _ := n.ListenPacket("10.0.0.2:0")
		defer c.Close()
		_, _ = c.WriteTo([]byte("WING?"), &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: DefaultPort})
		buf := make([]byte, 512)
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		k, _, err := c.ReadFrom(buf)
		if err != nil || !strings.HasPrefix(string(buf[:k]), "WING,10.0.0.1,PGM,ngc-full,") || strings.Count(string(buf[:k]), ",") != 5 {
			t.Fatalf("reply %q %v (WG-F11)", buf[:k], err)
		}
	})
}
