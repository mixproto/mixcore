// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package netpkt dissects captured frames down to the UDP or TCP payload, and
// builds raw IPv4/IPv6 packets for recordings made by the relay. It knows the
// link types tcpdump writes on macOS and Linux (Ethernet with VLAN tags, BSD
// loopback, raw IP, Linux cooked capture) and nothing above TCP/UDP.
//
// Sources: LINKTYPE values from https://www.tcpdump.org/linktypes.html;
// IPv4 RFC 791, IPv6 RFC 8200, UDP RFC 768, TCP RFC 9293.
package netpkt

import (
	"errors"
	"net/netip"
)

// Link types (LINKTYPE_* of the pcap formats).
const (
	LinkNull      uint16 = 0   // BSD loopback, 4-byte family in host byte order
	LinkEthernet  uint16 = 1   // Ethernet II
	LinkRawAlt    uint16 = 12  // raw IP on some BSDs (DLT_RAW)
	LinkRaw       uint16 = 101 // raw IPv4 or IPv6
	LinkLoop      uint16 = 108 // OpenBSD loopback, family in network byte order
	LinkLinuxSLL  uint16 = 113 // Linux cooked capture v1
	LinkIPv4      uint16 = 228
	LinkIPv6      uint16 = 229
	LinkLinuxSLL2 uint16 = 276 // Linux cooked capture v2
)

// Transport is the layer-4 protocol of a packet.
type Transport string

// Transports this package dissects.
const (
	UDP Transport = "udp"
	TCP Transport = "tcp"
)

// TCP flags (RFC 9293 section 3.1).
const (
	FlagFIN uint8 = 0x01
	FlagSYN uint8 = 0x02
	FlagRST uint8 = 0x04
	FlagPSH uint8 = 0x08
	FlagACK uint8 = 0x10
)

// Packet is a dissected UDP datagram or TCP segment. Payload aliases the frame.
type Packet struct {
	Transport Transport
	Src, Dst  netip.AddrPort
	Payload   []byte
	Seq       uint32 // TCP only
	Flags     uint8  // TCP only
}

// Sentinel errors. ErrSkip marks frames that are valid but carry nothing for
// us (ARP, ICMP, IP fragments); callers count them and move on.
var (
	ErrSkip      = errors.New("netpkt: not a UDP or TCP packet")
	ErrTruncated = errors.New("netpkt: truncated frame")
	ErrLinkType  = errors.New("netpkt: unsupported link type")
)
