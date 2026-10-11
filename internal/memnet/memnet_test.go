// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package memnet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func udp(s string) *net.UDPAddr { return net.UDPAddrFromAddrPort(netip.MustParseAddrPort(s)) }

func TestUnicastAndReplyAddress(t *testing.T) {
	n := New()
	a, _ := n.ListenPacket("10.0.0.1:10023")
	b, _ := n.ListenPacket("10.0.0.2:0")
	defer a.Close()
	defer b.Close()
	if _, err := b.WriteTo([]byte("ping"), udp("10.0.0.1:10023")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	k, from, err := a.ReadFrom(buf)
	if err != nil || string(buf[:k]) != "ping" || from.String() != b.LocalAddr().String() {
		t.Fatalf("got %q from %v, %v", buf[:k], from, err)
	}
}

func TestBroadcastReachesEveryListenerOnThePort(t *testing.T) {
	n := New()
	c1, _ := n.ListenPacket("10.0.0.1:10023")
	c2, _ := n.ListenPacket("10.0.0.3:10023")
	probe, _ := n.ListenPacket("10.0.0.2:0")
	defer c1.Close()
	defer c2.Close()
	defer probe.Close()
	if _, err := probe.WriteTo([]byte("x"), udp("255.255.255.255:10023")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*PacketConn{c1, c2} {
		buf := make([]byte, 4)
		if _, _, err := c.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeadlineAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := New()
		c, _ := n.ListenPacket("10.0.0.1:0")
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := c.ReadFrom(make([]byte, 4)); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("err = %v, want deadline exceeded", err)
		}
		_ = c.SetReadDeadline(time.Time{})
		ctx, cancel := context.WithCancel(t.Context())
		context.AfterFunc(ctx, func() { _ = c.SetReadDeadline(time.Now()) })
		done := make(chan error)
		go func() { _, _, err := c.ReadFrom(make([]byte, 4)); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("AfterFunc unblock err = %v", err)
		}
		_ = c.Close()
		if _, _, err := c.ReadFrom(make([]byte, 4)); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("read after close err = %v", err)
		}
		if _, err := c.WriteTo([]byte("x"), udp("10.0.0.9:1")); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("write after close err = %v", err)
		}
	})
}

func TestSetDownDropsTraffic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := New()
		a, _ := n.ListenPacket("10.0.0.1:10023")
		b, _ := n.ListenPacket("10.0.0.2:0")
		defer a.Close()
		defer b.Close()
		n.SetDown("10.0.0.1", true)
		_, _ = b.WriteTo([]byte("lost"), udp("10.0.0.1:10023"))
		_ = a.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := a.ReadFrom(make([]byte, 8)); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("packet to a down host was delivered: %v", err)
		}
	})
}

func TestAddressErrors(t *testing.T) {
	n := New()
	if _, err := n.ListenPacket("nonsense"); !errors.Is(err, ErrBadAddr) {
		t.Fatalf("err = %v", err)
	}
	c, _ := n.ListenPacket("10.0.0.1:5")
	defer c.Close()
	if _, err := n.ListenPacket("10.0.0.1:5"); !errors.Is(err, ErrAddrInUse) {
		t.Fatalf("err = %v", err)
	}
	listen := n.Host("10.0.0.7")
	pc, err := listen(t.Context(), "udp4", ":0")
	if err != nil || pc.LocalAddr().(*net.UDPAddr).IP.String() != "10.0.0.7" {
		t.Fatalf("Host listen = %v, %v", pc, err)
	}
	_ = pc.Close()
}
