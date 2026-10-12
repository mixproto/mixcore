// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package netpkt

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

// Protocol numbers and header sizes.
const (
	etherTypeIPv4  = 0x0800
	etherTypeIPv6  = 0x86dd
	etherTypeVLAN  = 0x8100
	etherTypeQinQ  = 0x88a8
	ethHeaderLen   = 14
	vlanTagLen     = 4
	maxVLANTags    = 2
	sllHeaderLen   = 16
	sll2HeaderLen  = 20
	nullHeaderLen  = 4
	afInet         = 2
	ipProtoTCP     = 6
	ipProtoUDP     = 17
	ipv4MinHdr     = 20
	ipv6HdrLen     = 40
	udpHdrLen      = 8
	tcpMinHdr      = 20
	ipv6HopByHop   = 0
	ipv6Routing    = 43
	ipv6Fragment   = 44
	ipv6DestOpts   = 60
	maxIPv6ExtHdrs = 8
)

// Dissect returns the UDP or TCP packet inside frame.
func Dissect(linkType uint16, frame []byte) (Packet, error) {
	ip, err := linkPayload(linkType, frame)
	if err != nil {
		return Packet{}, err
	}
	if len(ip) == 0 {
		return Packet{}, ErrTruncated
	}
	switch ip[0] >> 4 {
	case 4:
		return ipv4(ip)
	case 6:
		return ipv6(ip)
	default:
		return Packet{}, ErrSkip
	}
}

// linkPayload strips the link header and returns the IP packet.
func linkPayload(linkType uint16, f []byte) ([]byte, error) {
	switch linkType {
	case LinkRaw, LinkRawAlt, LinkIPv4, LinkIPv6:
		return f, nil
	case LinkNull, LinkLoop:
		if len(f) < nullHeaderLen {
			return nil, ErrTruncated
		}
		return f[nullHeaderLen:], nil // the IP version nibble tells v4 from v6
	case LinkEthernet:
		return ethernet(f)
	case LinkLinuxSLL:
		return withEtherType(f, sllHeaderLen, sllHeaderLen-2)
	case LinkLinuxSLL2:
		return withEtherType(f, sll2HeaderLen, 0)
	default:
		return nil, fmt.Errorf("%w: %d", ErrLinkType, linkType)
	}
}

func withEtherType(f []byte, hdrLen, typeAt int) ([]byte, error) {
	if len(f) < hdrLen {
		return nil, ErrTruncated
	}
	if !isIPEtherType(binary.BigEndian.Uint16(f[typeAt:])) {
		return nil, ErrSkip
	}
	return f[hdrLen:], nil
}

func isIPEtherType(t uint16) bool { return t == etherTypeIPv4 || t == etherTypeIPv6 }

func ethernet(f []byte) ([]byte, error) {
	if len(f) < ethHeaderLen {
		return nil, ErrTruncated
	}
	at := ethHeaderLen - 2
	for range maxVLANTags {
		t := binary.BigEndian.Uint16(f[at:])
		if t != etherTypeVLAN && t != etherTypeQinQ {
			break
		}
		at += vlanTagLen
		if len(f) < at+2 {
			return nil, ErrTruncated
		}
	}
	if !isIPEtherType(binary.BigEndian.Uint16(f[at:])) {
		return nil, ErrSkip
	}
	return f[at+2:], nil
}

func ipv4(b []byte) (Packet, error) {
	if len(b) < ipv4MinHdr {
		return Packet{}, ErrTruncated
	}
	ihl := int(b[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if ihl < ipv4MinHdr || total < ihl || len(b) < ihl {
		return Packet{}, ErrTruncated
	}
	const moreFragments, offsetMask = 0x2000, 0x1fff
	if frag := binary.BigEndian.Uint16(b[6:8]); frag&(moreFragments|offsetMask) != 0 {
		return Packet{}, ErrSkip // fragments are not reassembled
	}
	end := min(total, len(b)) // Ethernet padding is cut; a short capture keeps what it has
	src, _ := netip.AddrFromSlice(b[12:16])
	dst, _ := netip.AddrFromSlice(b[16:20])
	return transport(b[9], src, dst, b[ihl:end])
}

func ipv6(b []byte) (Packet, error) {
	if len(b) < ipv6HdrLen {
		return Packet{}, ErrTruncated
	}
	payloadLen := int(binary.BigEndian.Uint16(b[4:6]))
	next := b[6]
	src, _ := netip.AddrFromSlice(b[8:24])
	dst, _ := netip.AddrFromSlice(b[24:40])
	rest := b[ipv6HdrLen:min(len(b), ipv6HdrLen+payloadLen)]
	for range maxIPv6ExtHdrs {
		switch next {
		case ipv6HopByHop, ipv6Routing, ipv6DestOpts:
			if len(rest) < 8 {
				return Packet{}, ErrTruncated
			}
			n := (int(rest[1]) + 1) * 8
			if len(rest) < n {
				return Packet{}, ErrTruncated
			}
			next, rest = rest[0], rest[n:]
		case ipv6Fragment:
			return Packet{}, ErrSkip
		default:
			return transport(next, src, dst, rest)
		}
	}
	return Packet{}, ErrSkip
}

func transport(proto byte, src, dst netip.Addr, b []byte) (Packet, error) {
	switch proto {
	case ipProtoUDP:
		if len(b) < udpHdrLen {
			return Packet{}, ErrTruncated
		}
		n := int(binary.BigEndian.Uint16(b[4:6]))
		if n < udpHdrLen {
			return Packet{}, ErrTruncated
		}
		return Packet{
			Transport: UDP, Src: port(src, b[0:2]), Dst: port(dst, b[2:4]),
			Payload: b[udpHdrLen:min(n, len(b))],
		}, nil
	case ipProtoTCP:
		if len(b) < tcpMinHdr {
			return Packet{}, ErrTruncated
		}
		off := int(b[12]>>4) * 4
		if off < tcpMinHdr || off > len(b) {
			return Packet{}, ErrTruncated
		}
		return Packet{
			Transport: TCP, Src: port(src, b[0:2]), Dst: port(dst, b[2:4]),
			Seq: binary.BigEndian.Uint32(b[4:8]), Flags: b[13], Payload: b[off:],
		}, nil
	default:
		return Packet{}, ErrSkip
	}
}

func port(a netip.Addr, b []byte) netip.AddrPort {
	return netip.AddrPortFrom(a.Unmap(), binary.BigEndian.Uint16(b))
}
