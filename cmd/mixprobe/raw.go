// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
)

const rxBufSize = 4096

// rxMsg is one decoded message with its arrival time. Arg owns its bytes.
type rxMsg struct {
	At      time.Time
	Address string
	Arg     osc.Arg
	HasArg  bool
	Strings []string // every string argument, in order
}

// rawClient is one UDP socket that talks plain OSC to the console. Each
// rawClient is a separate source port, i.e. a separate client to the console
// (X32-F28). It never writes a parameter by itself: writes go through guard.
type rawClient struct {
	pc      net.PacketConn
	console netip.AddrPort
	to      net.Addr
	tx      []byte
	rx      []byte
	msg     osc.Message
}

func openRaw(ctx context.Context, listen driver.ListenPacketFunc, console netip.AddrPort) (*rawClient, error) {
	pc, err := listen(ctx, "udp4", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("mixprobe: open socket: %w", err)
	}
	return &rawClient{pc: pc, console: console, to: net.UDPAddrFromAddrPort(console), rx: make([]byte, rxBufSize)}, nil
}

func (c *rawClient) close() { _ = c.pc.Close() }

// send sends one message (no arguments = a read or a subscription).
func (c *rawClient) send(addr string, args ...osc.Arg) error {
	pkt, err := osc.AppendString(c.tx[:0], addr, args...)
	if err != nil {
		return fmt.Errorf("mixprobe: encode %s: %w", addr, err)
	}
	c.tx = pkt
	if _, err := c.pc.WriteTo(pkt, c.to); err != nil {
		return fmt.Errorf("mixprobe: send %s: %w", addr, err)
	}
	return nil
}

// recv returns the next message from the console, or ok=false after wait.
func (c *rawClient) recv(wait time.Duration) (rxMsg, bool, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := c.pc.SetReadDeadline(deadline); err != nil {
			return rxMsg{}, false, fmt.Errorf("mixprobe: deadline: %w", err)
		}
		n, from, err := c.pc.ReadFrom(c.rx)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return rxMsg{}, false, nil
		}
		if err != nil {
			return rxMsg{}, false, fmt.Errorf("mixprobe: read: %w", err)
		}
		if !c.fromConsole(from) || osc.Decode(c.rx[:n], &c.msg) != nil {
			continue
		}
		m := rxMsg{At: time.Now(), Address: string(c.msg.Address)}
		if a, ok := c.msg.Arg(0); ok {
			a.Bytes = slices.Clone(a.Bytes)
			m.Arg, m.HasArg = a, true
		}
		for _, a := range c.msg.Args() {
			if a.Type == osc.TypeString {
				m.Strings = append(m.Strings, string(a.Bytes))
			}
		}
		return m, true, nil
	}
}

func (c *rawClient) fromConsole(a net.Addr) bool {
	ua, ok := a.(*net.UDPAddr)
	if !ok {
		return false
	}
	ap := ua.AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()) == c.console
}

// read sends a read of addr and waits for the reply with that address.
func (c *rawClient) read(addr string, wait time.Duration) (osc.Arg, bool, error) {
	if err := c.send(addr); err != nil {
		return osc.Arg{}, false, err
	}
	deadline := time.Now().Add(wait)
	for left := wait; left > 0; left = time.Until(deadline) {
		m, ok, err := c.recv(left)
		if err != nil || !ok {
			return osc.Arg{}, false, err
		}
		if m.Address == addr && m.HasArg {
			return m.Arg, true, nil
		}
	}
	return osc.Arg{}, false, nil
}

// collect returns every message received during d.
func (c *rawClient) collect(d time.Duration) ([]rxMsg, error) {
	var out []rxMsg
	deadline := time.Now().Add(d)
	for left := d; left > 0; left = time.Until(deadline) {
		m, ok, err := c.recv(left)
		if err != nil {
			return out, err
		}
		if ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// waitFor returns the first message with address addr within wait.
func (c *rawClient) waitFor(addr string, wait time.Duration) (rxMsg, bool, error) {
	deadline := time.Now().Add(wait)
	for left := wait; left > 0; left = time.Until(deadline) {
		m, ok, err := c.recv(left)
		if err != nil || !ok {
			return rxMsg{}, false, err
		}
		if m.Address == addr {
			return m, true, nil
		}
	}
	return rxMsg{}, false, nil
}

// argString renders a wire argument for the report.
func argString(a osc.Arg) string {
	switch a.Type {
	case osc.TypeInt:
		return fmt.Sprintf("i %d", a.Int)
	case osc.TypeFloat:
		return fmt.Sprintf("f %.7f", a.Float)
	case osc.TypeString:
		return fmt.Sprintf("s %q", a.Bytes)
	case osc.TypeBlob:
		return fmt.Sprintf("b %d bytes", len(a.Bytes))
	default:
		return "?"
	}
}
