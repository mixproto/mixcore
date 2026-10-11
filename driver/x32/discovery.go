// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

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

// Found is one console that answered a discovery probe.
type Found struct {
	Endpoint driver.Endpoint
	Identity driver.Identity
}

// LimitedBroadcast is the all-hosts broadcast target on the control port (X32-F12).
var LimitedBroadcast = netip.AddrPortFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), DefaultPort)

// BroadcastTargets returns the limited broadcast address plus the directed
// broadcast address of every up, broadcast-capable IPv4 interface, so that a
// probe reaches consoles on every network the device is on, not only en0.
func BroadcastTargets() []netip.AddrPort {
	out := []netip.AddrPort{LimitedBroadcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if bc, ok := directedBroadcast(a); ok && !slices.Contains(out, bc) {
				out = append(out, bc)
			}
		}
	}
	return out
}

func directedBroadcast(a net.Addr) (netip.AddrPort, bool) {
	ipn, ok := a.(*net.IPNet)
	if !ok {
		return netip.AddrPort{}, false
	}
	ip4, mask := ipn.IP.To4(), ipn.Mask
	if ip4 == nil || len(mask) != net.IPv4len {
		return netip.AddrPort{}, false
	}
	var b [4]byte
	for i := range b {
		b[i] = ip4[i] | ^mask[i]
	}
	return netip.AddrPortFrom(netip.AddrFrom4(b), DefaultPort), true
}

// Probe sends /xinfo to every target (broadcast addresses and manual IPs alike,
// X32-F12) from one unconnected socket and collects replies until wait has
// passed or ctx ends. It only reads; it never subscribes or writes. It fails
// only when the socket cannot be opened or no probe could be sent at all.
func Probe(ctx context.Context, listen driver.ListenPacketFunc, targets []netip.AddrPort, wait time.Duration) ([]Found, error) {
	if listen == nil {
		listen = listenFunc(driver.Options{})
	}
	pc, err := listen(ctx, "udp4", anyAddr) // unconnected: replies come from each console's own address (X32-F12)
	if err != nil {
		return nil, fmt.Errorf("x32 probe: listen: %w", err)
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

// sendProbes sends /xinfo to every target. A target that cannot be reached
// (for example a broadcast address the OS refuses) does not stop the others;
// only when every send fails is the probe an error.
func sendProbes(pc net.PacketConn, targets []netip.AddrPort) error {
	pkt, err := osc.AppendString(nil, addrXinfo)
	if err != nil {
		return err
	}
	var firstErr error
	sent := 0
	for _, t := range targets {
		if _, err := pc.WriteTo(pkt, net.UDPAddrFromAddrPort(t)); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("x32 probe: send to %s: %w", t, err)
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
	seen := map[netip.AddrPort]bool{}
	buf := make([]byte, rxBufSize)
	var m osc.Message
	for {
		n, from, err := pc.ReadFrom(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return found, nil
		}
		if err != nil {
			return found, fmt.Errorf("x32 probe: read: %w", err)
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok || osc.Decode(buf[:n], &m) != nil || !m.AddressIs(addrXinfo) {
			continue
		}
		ap := ua.AddrPort()
		ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
		ident, err := parseXinfo(&m)
		if err != nil || seen[ap] {
			continue
		}
		seen[ap] = true
		found = append(found, Found{
			Endpoint: driver.Endpoint{Host: ap.Addr().String(), Port: int(ap.Port())},
			Identity: ident,
		})
	}
}
