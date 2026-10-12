// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/memnet"
)

// Whatever host a driver asks for, the socket is on loopback and keeps the port.
func TestListen_BindsLoopbackWithTheRequestedPort(t *testing.T) {
	n := memnet.New()
	var asked string
	base := func(ctx context.Context, network, address string) (net.PacketConn, error) {
		asked = network + " " + address
		return n.Host(loopbackIP)(ctx, network, address)
	}
	pc, err := Listen(base)(context.Background(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if asked != "udp4 127.0.0.1:0" || !isLoopback(pc.LocalAddr()) {
		t.Fatalf("asked %q, bound %v", asked, pc.LocalAddr())
	}
	if _, err := Listen(base)(context.Background(), "udp4", "no-port"); err == nil {
		t.Fatal("an address without a port must be refused")
	}
}

// The guard never sends off the device and drops packets from it.
func TestListen_TalksToLoopbackOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		pc, err := Listen(n.Host(loopbackIP))(context.Background(), "udp4", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		lan := &net.UDPAddr{IP: net.ParseIP("192.168.1.62"), Port: 10023}
		if _, err := pc.WriteTo([]byte("x"), lan); !errors.Is(err, ErrNotLoopback) {
			t.Fatalf("send to the LAN: %v", err)
		}
		stranger, err := n.ListenPacket("10.0.0.9:5000")
		if err != nil {
			t.Fatal(err)
		}
		defer stranger.Close()
		local, err := n.ListenPacket("127.0.0.3:5000")
		if err != nil {
			t.Fatal(err)
		}
		defer local.Close()
		if _, err := stranger.WriteTo([]byte("lan"), pc.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		if _, err := local.WriteTo([]byte("lo"), pc.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		_ = pc.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 16)
		k, from, err := pc.ReadFrom(buf)
		if err != nil || string(buf[:k]) != "lo" || from.String() != "127.0.0.3:5000" {
			t.Fatalf("read %q from %v: %v; the LAN packet must be dropped", buf[:k], from, err)
		}
		if _, err := pc.WriteTo([]byte("ok"), from); err != nil {
			t.Fatalf("reply to loopback: %v", err)
		}
	})
}
