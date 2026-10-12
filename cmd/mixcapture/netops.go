// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"net"
	"net/netip"
)

// netOps opens the sockets of the relay and the introspection client, and
// says whether an address belongs to this machine. main uses the system
// network; unit tests use an in-memory network and pipes (no real sockets).
type netOps struct {
	listenPacket func(ctx context.Context, network, address string) (net.PacketConn, error)
	listen       func(ctx context.Context, network, address string) (net.Listener, error)
	dial         func(ctx context.Context, network, address string) (net.Conn, error)
	isLocal      func(netip.Addr) bool
}

func systemNet() netOps {
	var lc net.ListenConfig
	d := net.Dialer{Timeout: dialTimeout}
	return netOps{listenPacket: lc.ListenPacket, listen: lc.Listen, dial: d.DialContext, isLocal: isLocalAddr}
}

// isLocalAddr reports whether ip is assigned to one of this machine's interfaces.
func isLocalAddr(ip netip.Addr) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Unmap() == ip {
			return true
		}
	}
	return false
}
