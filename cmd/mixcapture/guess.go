// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/capture/pcapfile"
)

// ephemeralFloor: ports at or above it are client ports (IANA dynamic range).
const ephemeralFloor = 49152

// guessConsole picks the server in a capture: TCP SYN destinations count
// most, then UDP destinations on a non-ephemeral port contacted from
// distinct client endpoints.
func guessConsole(path string) (netip.Addr, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the operator names the capture to read
	if err != nil {
		return netip.Addr{}, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }()
	rd, err := pcapfile.NewReader(bufio.NewReader(f))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("read %s: %w", path, err)
	}
	score := map[netip.Addr]int{}
	peers := map[[2]netip.AddrPort]bool{}
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return netip.Addr{}, fmt.Errorf("read %s: %w", path, err)
		}
		p, err := netpkt.Dissect(rec.LinkType, rec.Data)
		if err != nil {
			continue
		}
		scorePacket(p, score, peers)
	}
	return best(score)
}

func scorePacket(p netpkt.Packet, score map[netip.Addr]int, peers map[[2]netip.AddrPort]bool) {
	const synWeight = 10
	if p.Transport == netpkt.TCP && p.Flags&netpkt.FlagSYN != 0 && p.Flags&netpkt.FlagACK == 0 {
		score[p.Dst.Addr()] += synWeight
		return
	}
	if p.Transport != netpkt.UDP || p.Dst.Port() >= ephemeralFloor || p.Dst.Port() <= p.Src.Port() {
		return
	}
	id := [2]netip.AddrPort{p.Src, p.Dst}
	if !peers[id] {
		peers[id] = true
		score[p.Dst.Addr()]++
	}
}

func best(score map[netip.Addr]int) (netip.Addr, error) {
	var top netip.Addr
	n := 0
	for ip, s := range score {
		if s > n || (s == n && ip.Less(top)) {
			top, n = ip, s
		}
	}
	if n == 0 {
		return netip.Addr{}, fmt.Errorf("%w: could not guess the console from the traffic; give -console", errUsage)
	}
	return top, nil
}
