// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package oscsim

import (
	"context"
	"errors"
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

// IsHeadampGain recognises /headamp/<n>/gain in both families' spellings
// (X32-F70 three digits, XA-F110 two) and nothing else.
func TestIsHeadampGain(t *testing.T) {
	for addr, want := range map[string]bool{
		"/headamp/000/gain": true, "/headamp/01/gain": true, "/headamp/17/gain": true,
		"/headamp/01/phantom": false, "/headamp//gain": false, "/headamp/01": false, "/ch/01/mix/fader": false, "/headamp/01/gain/x": false,
	} {
		if got := IsHeadampGain(addr); got != want {
			t.Errorf("IsHeadampGain(%q) = %v", addr, got)
		}
	}
}

// noFamily is a family without special addresses or meters.
type noFamily struct{}

func (noFamily) Special(time.Time, netip.AddrPort, *osc.Message) ([]Outgoing, bool) {
	return nil, false
}
func (noFamily) MeterPacket(string) []byte { return nil }

// The core's write rules (the oracle of CC-3): with SilentWrite a client's
// Gain write is applied, read back and logged but pushed to nobody; other
// writes and surface changes are pushed to every /xremote client.
func TestCore_SilentWritePushesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		pc, err := n.ListenPacket("10.0.0.1:10023")
		if err != nil {
			t.Fatal(err)
		}
		c := New(Config{Prefix: "test sim", SubscriptionTTL: 10 * time.Second, MaxPushClients: 4, Family: noFamily{}, MeterTTL: 10 * time.Second, MeterInterval: 50 * time.Millisecond,
			SilentWrite: IsHeadampGain, Params: map[string]*Value{"/headamp/000/gain": Level(144), "/ch/01/mix/on": Switch(1)}})
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { _ = c.Serve(ctx, pc) })
		t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
		writer, sub := client(t, n, "10.0.0.2"), client(t, n, "10.0.0.3")
		sub.send(t, "/xremote")
		synctest.Wait()
		writer.send(t, "/headamp/000/gain", osc.Float(0.5))
		if m, ok := sub.recv(t); ok {
			t.Fatalf("a silent write was pushed: %s", m)
		}
		if v, _ := c.Value("/headamp/000/gain"); v.Float != 0.5 || len(c.Writes()) != 1 || !c.Writes()[0].Accepted {
			t.Fatalf("not applied: %v, log %+v", v.Float, c.Writes())
		}
		writer.send(t, "/ch/01/mix/on", osc.Int(0))
		if m, ok := sub.recv(t); !ok || m != "/ch/01/mix/on" {
			t.Fatalf("another write must be pushed: %q %v", m, ok)
		}
		if err := c.SurfaceSet("/headamp/000/gain", osc.Float(0.25)); err != nil {
			t.Fatal(err)
		}
		if m, ok := sub.recv(t); !ok || m != "/headamp/000/gain" {
			t.Fatalf("a surface change must be pushed: %q %v", m, ok)
		}
	})
}

type testClient struct{ pc net.PacketConn }

func client(t *testing.T, n *memnet.Network, ip string) testClient {
	t.Helper()
	pc, err := n.ListenPacket(ip + ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return testClient{pc: pc}
}

func (c testClient) send(t *testing.T, addr string, args ...osc.Arg) {
	t.Helper()
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.pc.WriteTo(b, net.UDPAddrFromAddrPort(netip.MustParseAddrPort("10.0.0.1:10023"))); err != nil {
		t.Fatal(err)
	}
}

// recv returns the address of the next message, or false after 100 ms.
func (c testClient) recv(t *testing.T) (string, bool) {
	t.Helper()
	buf := make([]byte, 1024)
	_ = c.pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, _, err := c.pc.ReadFrom(buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	var m osc.Message
	if err := osc.Decode(buf[:n], &m); err != nil {
		t.Fatal(err)
	}
	return string(m.Address), true
}
