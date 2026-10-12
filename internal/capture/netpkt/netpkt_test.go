// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package netpkt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var (
	app4     = netip.MustParseAddrPort("192.168.2.3:50123")
	console4 = netip.MustParseAddrPort("192.168.1.62:10024")
	app6     = netip.MustParseAddrPort("[fe80::1]:50123")
	console6 = netip.MustParseAddrPort("[fe80::2]:49280")
)

func mustBuild(t testing.TB, p Packet) []byte {
	t.Helper()
	b, err := Build(p)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return b
}

func TestBuildDissectRoundTrip(t *testing.T) {
	tests := []Packet{
		{Transport: UDP, Src: app4, Dst: console4, Payload: []byte("/xinfo\x00\x00")},
		{Transport: TCP, Src: console4, Dst: app4, Seq: 1000, Flags: FlagPSH | FlagACK, Payload: []byte("OK\n")},
		{Transport: UDP, Src: app6, Dst: console6, Payload: []byte{1, 2, 3}},
		{Transport: TCP, Src: app6, Dst: console6, Seq: 7, Flags: FlagSYN},
	}
	for _, want := range tests {
		raw := mustBuild(t, want)
		got, err := Dissect(LinkRaw, raw)
		if err != nil {
			t.Fatalf("Dissect(%v): %v", want, err)
		}
		if got.Transport != want.Transport || got.Src != want.Src || got.Dst != want.Dst ||
			!bytes.Equal(got.Payload, want.Payload) || got.Seq != want.Seq || got.Flags != want.Flags {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
	}
}

func TestBuildChecksumsVerify(t *testing.T) {
	for _, p := range []Packet{
		{Transport: UDP, Src: app4, Dst: console4, Payload: []byte("odd")},
		{Transport: TCP, Src: app4, Dst: console4, Seq: 1, Flags: FlagACK, Payload: []byte("even")},
		{Transport: UDP, Src: app6, Dst: console6, Payload: []byte("v6")},
	} {
		raw := mustBuild(t, p)
		hdr := ipv6HdrLen
		if p.Src.Addr().Is4() {
			hdr = ipv4MinHdr
			if checksum(0, raw[:hdr]) != 0 {
				t.Fatalf("IPv4 header checksum does not verify")
			}
		}
		proto := byte(ipProtoUDP)
		if p.Transport == TCP {
			proto = ipProtoTCP
		}
		seg := raw[hdr:]
		if checksum(pseudoSum(p.Src.Addr(), p.Dst.Addr(), proto, len(seg)), seg) != 0 {
			t.Fatalf("%s checksum does not verify", p.Transport)
		}
	}
}

func TestBuildRejectsMixedFamilies(t *testing.T) {
	if _, err := Build(Packet{Transport: UDP, Src: app4, Dst: console6}); !errors.Is(err, ErrBuild) {
		t.Fatalf("err = %v, want ErrBuild", err)
	}
}

func ether(etherType uint16, vlan bool, payload []byte) []byte {
	f := make([]byte, 12) // destination and source MAC
	if vlan {
		f = binary.BigEndian.AppendUint16(f, etherTypeVLAN)
		f = binary.BigEndian.AppendUint16(f, 42)
	}
	f = binary.BigEndian.AppendUint16(f, etherType)
	return append(f, payload...)
}

func TestDissectLinkTypes(t *testing.T) {
	ip := mustBuild(t, Packet{Transport: UDP, Src: app4, Dst: console4, Payload: []byte("abcd")})
	ip6 := mustBuild(t, Packet{Transport: UDP, Src: app6, Dst: console6, Payload: []byte("abcd")})
	padded := append(ether(etherTypeIPv4, false, ip), 0, 0, 0, 0) // Ethernet minimum-size padding
	sll := append(make([]byte, 14), 0x08, 0x00)
	sll2 := append([]byte{0x86, 0xdd}, make([]byte, 18)...)
	tests := []struct {
		name string
		link uint16
		f    []byte
	}{
		{"ethernet", LinkEthernet, ether(etherTypeIPv4, false, ip)},
		{"ethernet padding is cut", LinkEthernet, padded},
		{"ethernet vlan", LinkEthernet, ether(etherTypeIPv4, true, ip)},
		{"ethernet ipv6", LinkEthernet, ether(etherTypeIPv6, false, ip6)},
		{"bsd loopback", LinkNull, append([]byte{2, 0, 0, 0}, ip...)},
		{"openbsd loop", LinkLoop, append([]byte{0, 0, 0, 2}, ip...)},
		{"ipv4 link", LinkIPv4, ip},
		{"linux sll", LinkLinuxSLL, append(sll, ip...)},
		{"linux sll2", LinkLinuxSLL2, append(sll2, ip6...)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Dissect(tc.link, tc.f)
			if err != nil || string(p.Payload) != "abcd" || p.Transport != UDP {
				t.Fatalf("Dissect = %+v, %v", p, err)
			}
		})
	}
}

func TestDissectSkipsAndRejects(t *testing.T) {
	ip := mustBuild(t, Packet{Transport: UDP, Src: app4, Dst: console4, Payload: []byte("abcd")})
	frag := bytes.Clone(ip)
	binary.BigEndian.PutUint16(frag[6:8], 0x2000) // more fragments
	icmp := bytes.Clone(ip)
	icmp[9] = 1
	tests := []struct {
		name string
		link uint16
		f    []byte
		want error
	}{
		{"arp", LinkEthernet, ether(0x0806, false, make([]byte, 28)), ErrSkip},
		{"fragment", LinkRaw, frag, ErrSkip},
		{"icmp", LinkRaw, icmp, ErrSkip},
		{"short ethernet", LinkEthernet, make([]byte, 6), ErrTruncated},
		{"short ip", LinkRaw, ip[:10], ErrTruncated},
		{"short udp", LinkRaw, ip[:ipv4MinHdr+4], ErrTruncated},
		{"unknown link", 9999, ip, ErrLinkType},
		{"empty", LinkRaw, nil, ErrTruncated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Dissect(tc.link, tc.f); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// FuzzDissect: arbitrary frames on every supported link type never panic and
// a payload always lies inside the frame.
func FuzzDissect(f *testing.F) {
	ip := mustBuild(f, Packet{Transport: TCP, Src: app4, Dst: console4, Seq: 9, Flags: FlagACK, Payload: []byte("x")})
	f.Add(LinkRaw, ip)
	f.Add(LinkEthernet, ether(etherTypeIPv4, true, ip))
	f.Add(LinkRaw, mustBuild(f, Packet{Transport: UDP, Src: app6, Dst: console6, Payload: []byte("y")}))
	f.Fuzz(func(t *testing.T, link uint16, frame []byte) {
		p, err := Dissect(link, frame)
		if err != nil {
			return
		}
		if len(p.Payload) > len(frame) {
			t.Fatalf("payload of %d bytes from a %d-byte frame", len(p.Payload), len(frame))
		}
	})
}
