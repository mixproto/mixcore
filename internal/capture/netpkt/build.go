// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package netpkt

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// Header values of the packets Build writes.
const (
	defaultTTL   = 64
	maxIPPayload = 65535 - ipv6HdrLen
	tcpWindow    = 65535
)

// ErrBuild is returned for a packet Build cannot represent.
var ErrBuild = errors.New("netpkt: cannot build packet")

// Build returns a raw IP packet (LinkRaw) carrying p. Both addresses must be
// the same family. Checksums are filled in, so Wireshark shows no warnings.
func Build(p Packet) ([]byte, error) {
	src, dst := p.Src.Addr().Unmap(), p.Dst.Addr().Unmap()
	if src.Is4() != dst.Is4() || !src.IsValid() || !dst.IsValid() {
		return nil, ErrBuild
	}
	seg, proto := segment(p)
	if len(seg) > maxIPPayload {
		return nil, ErrBuild
	}
	var out []byte
	if src.Is4() {
		out = ipv4Header(src, dst, proto, len(seg))
	} else {
		out = ipv6Header(src, dst, proto, len(seg))
	}
	sum := pseudoSum(src, dst, proto, len(seg))
	ck := checksum(sum, seg)
	if ck == 0 && proto == ipProtoUDP {
		ck = 0xffff // RFC 768: a computed zero is sent as all ones
	}
	at := 6 // UDP checksum offset
	if proto == ipProtoTCP {
		at = 16
	}
	binary.BigEndian.PutUint16(seg[at:], ck)
	return append(out, seg...), nil
}

func segment(p Packet) ([]byte, byte) {
	if p.Transport == TCP {
		b := make([]byte, tcpMinHdr, tcpMinHdr+len(p.Payload))
		binary.BigEndian.PutUint16(b[0:2], p.Src.Port())
		binary.BigEndian.PutUint16(b[2:4], p.Dst.Port())
		binary.BigEndian.PutUint32(b[4:8], p.Seq)
		b[12] = (tcpMinHdr / 4) << 4
		b[13] = p.Flags
		binary.BigEndian.PutUint16(b[14:16], tcpWindow)
		return append(b, p.Payload...), ipProtoTCP
	}
	b := make([]byte, udpHdrLen, udpHdrLen+len(p.Payload))
	binary.BigEndian.PutUint16(b[0:2], p.Src.Port())
	binary.BigEndian.PutUint16(b[2:4], p.Dst.Port())
	binary.BigEndian.PutUint16(b[4:6], uint16(udpHdrLen+len(p.Payload)))
	return append(b, p.Payload...), ipProtoUDP
}

func ipv4Header(src, dst netip.Addr, proto byte, n int) []byte {
	h := make([]byte, ipv4MinHdr, ipv4MinHdr+n)
	h[0] = 0x45
	binary.BigEndian.PutUint16(h[2:4], uint16(ipv4MinHdr+n))
	h[8] = defaultTTL
	h[9] = proto
	s, d := src.As4(), dst.As4()
	copy(h[12:16], s[:])
	copy(h[16:20], d[:])
	binary.BigEndian.PutUint16(h[10:12], checksum(0, h))
	return h
}

func ipv6Header(src, dst netip.Addr, proto byte, n int) []byte {
	h := make([]byte, ipv6HdrLen, ipv6HdrLen+n)
	h[0] = 0x60
	binary.BigEndian.PutUint16(h[4:6], uint16(n))
	h[6] = proto
	h[7] = defaultTTL
	s, d := src.As16(), dst.As16()
	copy(h[8:24], s[:])
	copy(h[24:40], d[:])
	return h
}

// pseudoSum is the one's-complement sum of the TCP/UDP pseudo-header.
func pseudoSum(src, dst netip.Addr, proto byte, n int) uint32 {
	var sum uint32
	add := func(b []byte) {
		for i := 0; i+1 < len(b); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(b[i:]))
		}
	}
	add(src.AsSlice())
	add(dst.AsSlice())
	return sum + uint32(proto) + uint32(n)
}

// checksum folds sum plus the 16-bit words of b (RFC 1071).
func checksum(sum uint32, b []byte) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
