// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const simAddr = "10.0.0.1:10024" // XA-F01

var simUDP = net.UDPAddrFromAddrPort(netip.MustParseAddrPort(simAddr))

// harness runs a simulator over memnet; tests run it inside synctest.
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

// msg is a received message with its own copy of the first argument.
type msg struct {
	addr    string
	arg     osc.Arg
	strings []string
}

// recv returns the next message or ok=false after wait.
func (c *client) recv(wait time.Duration) (msg, bool) {
	c.t.Helper()
	buf := make([]byte, 2048)
	_ = c.pc.SetReadDeadline(time.Now().Add(wait))
	n, _, err := c.pc.ReadFrom(buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return msg{}, false
	}
	if err != nil {
		c.t.Fatal(err)
	}
	var m osc.Message
	if err := osc.Decode(buf[:n], &m); err != nil {
		c.t.Fatalf("simulator sent an undecodable packet: %v", err)
	}
	out := msg{addr: string(m.Address)}
	if a, ok := m.Arg(0); ok {
		a.Bytes = slices.Clone(a.Bytes)
		out.arg = a
	}
	for _, a := range m.Args() {
		if a.Type == osc.TypeString {
			out.strings = append(out.strings, string(a.Bytes))
		}
	}
	return out, true
}

// read sends a read and returns the reply, ok=false when none came.
func (c *client) read(addr string) (osc.Arg, bool) {
	c.t.Helper()
	c.send(addr)
	m, ok := c.recv(100 * time.Millisecond)
	if !ok || m.addr != addr {
		return osc.Arg{}, false
	}
	return m.arg, true
}

// all returns every message received within wait.
func (c *client) all(wait time.Duration) []msg {
	c.t.Helper()
	var out []msg
	deadline := time.Now().Add(wait)
	for left := wait; left > 0; left = time.Until(deadline) {
		m, ok := c.recv(left)
		if !ok {
			break
		}
		out = append(out, m)
	}
	return out
}
