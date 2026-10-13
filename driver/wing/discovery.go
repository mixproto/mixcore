// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/internal/xosc"
)

// Discovery (WG-F11): the 5 ASCII bytes "WING?" to UDP 2222, unicast or
// broadcast; the console answers the requester with one comma-separated
// datagram "WING,<ip>,<name>,<model>,<serial>,<firmware>".
const (
	discoveryRequest = "WING?"
	identityPrefix   = "WING,"
	identityFields   = 6
	maxIdentityLen   = 512 // far above any documented reply
	discoveryBuf     = 1024
)

// Found is one console that answered a discovery probe (the same type as
// the other families', so callers list every family alike).
type Found = xosc.Found

// errBadIdentity: a datagram is not a WING identity reply.
var errBadIdentity = errors.New("wing: not a WING identity reply")

// BroadcastTargets returns the limited broadcast address and the directed
// broadcast of every up, broadcast-capable IPv4 interface on UDP 2222, so a
// probe reaches consoles on every network the device is on.
func BroadcastTargets() []netip.AddrPort { return xosc.BroadcastTargets(DefaultPort) }

// ParseIdentity reads a discovery reply (WG-F11). The model and firmware
// strings are kept as the console sent them: the driver accepts any model
// string (WG-F04). Our own broadcast "WING?" and anything else is rejected.
func ParseIdentity(b []byte) (driver.Identity, error) {
	if len(b) > maxIdentityLen || !utf8.Valid(b) || !strings.HasPrefix(string(b), identityPrefix) {
		return driver.Identity{}, errBadIdentity
	}
	f := strings.Split(strings.TrimRight(string(b), "\x00\r\n"), ",")
	if len(f) != identityFields {
		return driver.Identity{}, fmt.Errorf("%w: %d fields", errBadIdentity, len(f))
	}
	return driver.Identity{Address: f[1], Name: f[2], Model: f[3], Firmware: f[5]}, nil
}

// Probe sends "WING?" to every target (broadcast addresses and manual IPs
// alike, each with its own port) from one unconnected socket and collects
// replies until wait has passed or ctx ends. It only reads (WG-F11). It
// fails only when the socket cannot be opened or no probe could be sent.
// The endpoint of a reply is its source address on the control port.
func Probe(ctx context.Context, listen driver.ListenPacketFunc, targets []netip.AddrPort, wait time.Duration) ([]Found, error) {
	if listen == nil {
		listen = listenFunc(driver.Options{})
	}
	pc, err := listen(ctx, "udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(0)))
	if err != nil {
		return nil, fmt.Errorf("wing probe: listen: %w", err)
	}
	defer pc.Close()
	if err := sendProbes(pc, targets); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := pc.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()
	return collectReplies(pc)
}

func sendProbes(pc net.PacketConn, targets []netip.AddrPort) error {
	var firstErr error
	sent := 0
	for _, t := range targets {
		if _, err := pc.WriteTo([]byte(discoveryRequest), net.UDPAddrFromAddrPort(t)); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("wing probe: send to %s: %w", t, err)
			}
			continue
		}
		sent++
	}
	if sent == 0 && firstErr != nil {
		return firstErr
	}
	return nil
}

func collectReplies(pc net.PacketConn) ([]Found, error) {
	var found []Found
	seen := map[netip.Addr]bool{}
	buf := make([]byte, discoveryBuf)
	for {
		n, from, err := pc.ReadFrom(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return found, nil
		}
		if err != nil {
			return found, fmt.Errorf("wing probe: read: %w", err)
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		ident, err := ParseIdentity(buf[:n])
		ip := ua.AddrPort().Addr().Unmap()
		if err != nil || seen[ip] {
			continue
		}
		seen[ip] = true
		found = append(found, Found{Endpoint: driver.Endpoint{Host: ip.String(), Port: DefaultPort}, Identity: ident})
	}
}
