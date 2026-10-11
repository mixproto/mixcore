// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const simAddr = "10.0.0.1:10023"

var simUDP = net.UDPAddrFromAddrPort(netip.MustParseAddrPort(simAddr))

// harness runs a simulator over memnet inside a synctest bubble.
type harness struct {
	t   *testing.T
	net *memnet.Network
	sim *Sim
}

func runSim(t *testing.T, opts Options) *harness {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(simAddr)
	if err != nil {
		t.Fatal(err)
	}
	s := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := s.Serve(ctx, pc); err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	return &harness{t: t, net: n, sim: s}
}

type client struct {
	t  *testing.T
	pc net.PacketConn
}

func (h *harness) client(ip string) *client {
	h.t.Helper()
	pc, err := h.net.ListenPacket(ip + ":0")
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = pc.Close() })
	return &client{t: h.t, pc: pc}
}

func (c *client) send(addr string, args ...osc.Arg) {
	c.t.Helper()
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.pc.WriteTo(b, simUDP); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) sendRaw(b []byte) {
	c.t.Helper()
	if _, err := c.pc.WriteTo(b, simUDP); err != nil {
		c.t.Fatal(err)
	}
}

// recv returns the next message or ok=false after wait.
func (c *client) recv(wait time.Duration) (osc.Message, bool) {
	c.t.Helper()
	buf := make([]byte, 2048)
	_ = c.pc.SetReadDeadline(time.Now().Add(wait))
	n, _, err := c.pc.ReadFrom(buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return osc.Message{}, false
	}
	if err != nil {
		c.t.Fatal(err)
	}
	var m osc.Message
	if err := osc.Decode(buf[:n], &m); err != nil {
		c.t.Fatalf("simulator sent an undecodable packet: %v", err)
	}
	return m, true
}

func (c *client) drain() {
	for {
		if _, ok := c.recv(time.Millisecond); !ok {
			return
		}
	}
}

func TestSim_DecodesTheDocumentedFaderPacket(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		pkt, _ := hex.DecodeString("2f63682f30312f6d69782f6661646572000000002c6600003f5334cd") // X32-F43
		c.sendRaw(pkt)
		synctest.Wait()
		v, _ := h.sim.Value("/ch/01/mix/fader")
		if math.Float32bits(v.Float) != 0x3f5334cd {
			t.Fatalf("ch01 fader = %#08x, want 0x3f5334cd", math.Float32bits(v.Float))
		}
		if w := h.sim.Writes(); len(w) != 1 || !w[0].Accepted {
			t.Fatalf("write log = %+v", w)
		}
	})
}

func TestSim_XinfoAndInfoReplies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{Name: "FOH", Model: "M32", Firmware: "4.06"})
		c := h.client("10.0.0.2")
		c.send("/xinfo")
		m, ok := c.recv(time.Second)
		if !ok || m.String() != `/xinfo ,ssss "10.0.0.1" "FOH" "M32" "4.06"` {
			t.Fatalf("xinfo reply = %v, %v", m.String(), ok)
		}
		c.send("/info")
		m, ok = c.recv(time.Second)
		if !ok || m.String() != `/info ,ssss "V2.05" "osc-server" "M32" "4.06"` {
			t.Fatalf("info reply = %v, %v", m.String(), ok)
		}
	})
}

func TestSim_ReadReturnsValueAndUnknownIsSilent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		c.send("/ch/05/mix/on")
		m, ok := c.recv(time.Second)
		if !ok || m.String() != "/ch/05/mix/on ,i 1" {
			t.Fatalf("read reply = %v", m.String())
		}
		c.send("/ch/40/mix/on")
		if m, ok := c.recv(time.Second); ok {
			t.Fatalf("unknown address must get no reply, got %s", m.String())
		}
	})
}

func TestSim_PushesGoToOtherSubscribersNotTheSender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		a, b := h.client("10.0.0.2"), h.client("10.0.0.3")
		a.send("/xremote")
		b.send("/xremote")
		synctest.Wait()
		a.send("/ch/01/mix/on", osc.Int(0))
		if m, ok := b.recv(time.Second); !ok || m.String() != "/ch/01/mix/on ,i 0" {
			t.Fatalf("other subscriber push = %v, %v", m.String(), ok)
		}
		if m, ok := a.recv(time.Second); ok {
			t.Fatalf("sender must not get its own change back (X32-F23), got %s", m.String())
		}
	})
}

func TestSim_EchoToSenderVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{EchoToSender: true})
		a := h.client("10.0.0.2")
		a.send("/xremote")
		a.send("/ch/01/mix/on", osc.Int(0))
		if _, ok := a.recv(time.Second); !ok {
			t.Fatal("EchoToSender variant must push to the sender")
		}
	})
}

func TestSim_FourPushClientsAndExpiry(t *testing.T) {
	for _, evict := range []bool{false, true} {
		t.Run(map[bool]string{false: "newcomer ignored (default)", true: "oldest evicted"}[evict], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := runSim(t, Options{EvictOldestWhenFull: evict})
				var cs []*client
				for i := range 5 {
					c := h.client("10.0.1." + string(rune('1'+i)))
					c.send("/xremote")
					synctest.Wait()
					cs = append(cs, c)
				}
				if n := len(h.sim.Subscribers()); n != DefaultMaxPushClients {
					t.Fatalf("%d subscribers, want %d (X32-F21)", n, DefaultMaxPushClients)
				}
				if err := h.sim.SurfaceSet("/ch/02/mix/fader", osc.Float(0.5)); err != nil {
					t.Fatal(err)
				}
				_, firstGot := cs[0].recv(time.Second)
				_, fifthGot := cs[4].recv(time.Second)
				if firstGot == evict || fifthGot != evict {
					t.Fatalf("first got push %v, fifth got push %v (evict=%v)", firstGot, fifthGot, evict)
				}
				time.Sleep(DefaultSubscriptionTTL)
				if n := len(h.sim.Subscribers()); n != 0 {
					t.Fatalf("%d subscribers after the 10 s TTL, want 0 (X32-F20)", n)
				}
			})
		})
	}
}

func TestSim_MetersStreamAndExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		h.sim.SetInputLevels([]float32{1, 0.5})
		c := h.client("10.0.0.2")
		c.send("/meters", osc.String("/meters/1"))
		m, ok := c.recv(time.Second)
		if !ok || !m.AddressIs("/meters/1") {
			t.Fatalf("meter packet = %v, %v", m.String(), ok)
		}
		blob, _ := m.BlobArg(0)
		if len(blob) != 4+4*meterBank1Values || binary.LittleEndian.Uint32(blob) != meterBank1Values {
			t.Fatalf("blob len %d count %d (X32-F53)", len(blob), binary.LittleEndian.Uint32(blob))
		}
		if v := math.Float32frombits(binary.LittleEndian.Uint32(blob[8:])); v != 0.5 {
			t.Fatalf("input 2 level = %v, want 0.5", v)
		}
		time.Sleep(DefaultMeterTTL + time.Second)
		c.drain()
		if _, ok := c.recv(time.Second); ok {
			t.Fatal("meters must stop 10 s after the last request (X32-F50)")
		}
	})
}

func TestSim_UnslashedBankAndTimeFactor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		c.send("/meters", osc.String("meters/1"), osc.Int(40)) // X32-F50, X32-F52
		count := 0
		deadline := time.Now().Add(DefaultMeterTTL)
		for time.Now().Before(deadline) {
			if _, ok := c.recv(time.Until(deadline)); ok {
				count++
			}
		}
		if count != 5 {
			t.Fatalf("time_factor 40 over 10 s gave %d packets, want 5 (X32-F50)", count)
		}
	})
}

func TestSim_PauseDropsTrafficAndRebootClearsSubscriptions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		c.send("/xremote")
		synctest.Wait()
		h.sim.Pause()
		c.send("/xinfo")
		if _, ok := c.recv(time.Second); ok {
			t.Fatal("paused console answered")
		}
		if err := h.sim.SurfaceSet("/ch/03/mix/on", osc.Int(0)); err != nil {
			t.Fatal(err)
		}
		h.sim.Resume()
		if v, _ := h.sim.Value("/ch/03/mix/on"); v.Int != 0 {
			t.Fatal("surface change during an outage must persist")
		}
		h.sim.Reboot(2 * time.Second)
		if len(h.sim.Subscribers()) != 0 {
			t.Fatal("reboot must drop /xremote clients (X32-F26)")
		}
		c.send("/xinfo")
		if _, ok := c.recv(time.Second); ok {
			t.Fatal("rebooting console answered")
		}
		time.Sleep(2 * time.Second)
		c.send("/xinfo")
		if _, ok := c.recv(time.Second); !ok {
			t.Fatal("console did not come back after reboot")
		}
	})
}

func TestSim_RejectsBadValuesButLogsThemAsWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := runSim(t, Options{})
		c := h.client("10.0.0.2")
		c.send("/ch/01/mix/on", osc.Int(7))
		c.send("/ch/01/mix/fader", osc.Int(1))
		c.send("/ch/01/config/name", osc.String("thirteen-char"))
		c.send("/nope", osc.Int(1))
		c.sendRaw([]byte("garbage"))
		synctest.Wait()
		w := h.sim.Writes()
		if len(w) != 4 {
			t.Fatalf("%d writes logged, want 4", len(w))
		}
		for _, e := range w {
			if e.Accepted {
				t.Fatalf("bad write accepted: %+v", e)
			}
		}
		if err := h.sim.SurfaceSet("/nope", osc.Int(1)); !errors.Is(err, ErrUnknownAddress) {
			t.Fatalf("SurfaceSet unknown err = %v", err)
		}
	})
}

func TestQuantizeLevel(t *testing.T) {
	tests := []struct{ in, want float32 }{
		{0.825, 844.0 / 1023}, {0.25, 256.0 / 1023}, {-1, 0}, {2, 1}, {0, 0},
	}
	for _, tc := range tests {
		if got := quantizeLevel(tc.in); got != tc.want {
			t.Fatalf("quantizeLevel(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if ClassWrite.String() != "write" || Class(0).String() != "invalid" {
		t.Fatal("Class.String mismatch")
	}
}
