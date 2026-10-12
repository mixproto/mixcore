// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package demo

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// On real OS sockets: the demo binds 127.0.0.1 with an OS-chosen port,
// answers /xinfo, and Stop frees the port at once.
func TestStart_OSLoopbackSocket(t *testing.T) {
	m, err := Start(Config{Family: FamilyXAir, Law: law})
	if err != nil {
		t.Fatal(err)
	}
	ep := m.Endpoint()
	if ep.Addr().String() != loopbackIP || ep.Port() == 0 {
		t.Fatalf("endpoint %v", ep)
	}
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pkt, _ := osc.AppendString(nil, "/xinfo")
	if _, err := c.WriteTo(pkt, net.UDPAddrFromAddrPort(ep)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, _, err := c.ReadFrom(buf)
	var msg osc.Message
	if err != nil || osc.Decode(buf[:n], &msg) != nil {
		t.Fatalf("no /xinfo reply: %v", err)
	}
	if name, _ := msg.StringArg(1); name != consoleName {
		t.Fatalf("name %q", name)
	}
	m.Stop()
	again, err := net.ListenPacket("udp4", ep.String())
	if err != nil {
		t.Fatalf("port %d still held after Stop: %v", ep.Port(), err)
	}
	_ = again.Close()
}

// On a real OS socket the guard refuses a send to the LAN before the OS sees it.
func TestListen_OSSocketRefusesTheLAN(t *testing.T) {
	pc, err := Listen(nil)(t.Context(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	lan := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 62), Port: 10023}
	if _, err := pc.WriteTo([]byte("x"), lan); !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("send to the LAN: %v", err)
	}
}
