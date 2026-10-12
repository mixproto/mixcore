// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package pcapfile reads classic pcap and pcapng capture files and writes
// classic pcap, in pure Go (no cgo, no libpcap). It is written from the file
// format descriptions, not from any library:
//
//   - pcap:   https://www.ietf.org/archive/id/draft-ietf-opsawg-pcap-04.html
//   - pcapng: https://www.ietf.org/archive/id/draft-ietf-opsawg-pcapng-02.html
//
// The reader streams (it works on a pipe from tcpdump), reuses one buffer and
// caps every length it reads from the file, so a corrupt or hostile file is
// rejected instead of exhausting memory.
package pcapfile

import (
	"errors"
	"time"
)

// Limits. Every length read from a file is checked against one of these.
const (
	// MaxPacket is the largest captured packet accepted (tcpdump's maximum snaplen).
	MaxPacket = 262144
	// maxSkipBlock is the largest pcapng block skipped without reading it.
	maxSkipBlock = 64 << 20
	// maxInterfaces is the largest number of pcapng interfaces in one section.
	maxInterfaces = 256
)

// File format constants.
const (
	magicMicro        = 0xa1b2c3d4
	magicNano         = 0xa1b23c4d
	ngBlockSHB        = 0x0a0d0d0a
	ngBlockIDB        = 0x00000001
	ngBlockOPB        = 0x00000002 // obsolete Packet Block
	ngBlockSPB        = 0x00000003
	ngBlockEPB        = 0x00000006
	ngByteOrderMagic  = 0x1a2b3c4d
	ngOptEnd          = 0
	ngOptTSResol      = 9
	ngOptTSOffset     = 14
	pcapGlobalHdrLen  = 24
	pcapRecordHdrLen  = 16
	ngBlockMinLen     = 12
	defaultTSResolExp = 6 // microseconds, pcapng default
)

// Sentinel errors. Compare with errors.Is.
var (
	ErrFormat   = errors.New("pcapfile: not a pcap or pcapng file")
	ErrCorrupt  = errors.New("pcapfile: corrupt file")
	ErrTooLarge = errors.New("pcapfile: length over the limit")
)

// Record is one captured packet.
type Record struct {
	At       time.Time
	LinkType uint16 // LINKTYPE_* of the interface the packet was captured on
	// Data is the captured bytes. It aliases the reader's buffer and is valid
	// only until the next call to Next.
	Data    []byte
	OrigLen int   // length on the wire (may exceed len(Data) when truncated)
	Offset  int64 // byte offset of the record header (pcap) or block (pcapng)
	Index   int   // 1-based packet number, as Wireshark counts
}
