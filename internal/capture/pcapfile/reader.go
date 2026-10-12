// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pcapfile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Reader reads packets from a pcap or pcapng stream.
type Reader struct {
	r      io.Reader
	off    int64
	index  int
	buf    []byte
	hdr    [pcapRecordHdrLen]byte
	isNG   bool
	pcap   pcapState
	ng     ngState
	format string
}

type pcapState struct {
	order    binary.ByteOrder
	nano     bool
	linkType uint16
}

// NewReader reads the file header and returns a reader positioned at the
// first packet. It accepts classic pcap (both byte orders, micro- and
// nanosecond magic) and pcapng.
func NewReader(r io.Reader) (*Reader, error) {
	rd := &Reader{r: r, buf: make([]byte, 0, 2048)}
	var magic [4]byte
	if err := rd.readFull(magic[:]); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if binary.BigEndian.Uint32(magic[:]) == ngBlockSHB {
		rd.isNG = true
		rd.format = "pcapng"
		if err := rd.readSHBAfterType(); err != nil {
			return nil, err
		}
		return rd, nil
	}
	if err := rd.readPcapHeader(magic); err != nil {
		return nil, err
	}
	rd.format = "pcap"
	return rd, nil
}

// Format is "pcap" or "pcapng".
func (rd *Reader) Format() string { return rd.format }

// Next returns the next packet, or io.EOF at a clean end of file. A file cut
// in the middle of a record returns io.ErrUnexpectedEOF.
func (rd *Reader) Next() (Record, error) {
	if rd.isNG {
		return rd.nextNG()
	}
	return rd.nextPcap()
}

func (rd *Reader) readFull(b []byte) error {
	n, err := io.ReadFull(rd.r, b)
	rd.off += int64(n)
	return err
}

// grow returns rd.buf resized to n bytes, reusing its storage.
func (rd *Reader) grow(n int) []byte {
	if cap(rd.buf) < n {
		rd.buf = make([]byte, n)
	}
	rd.buf = rd.buf[:n]
	return rd.buf
}

func (rd *Reader) readPcapHeader(magic [4]byte) error {
	var st pcapState
	switch {
	case binary.LittleEndian.Uint32(magic[:]) == magicMicro:
		st.order = binary.LittleEndian
	case binary.BigEndian.Uint32(magic[:]) == magicMicro:
		st.order = binary.BigEndian
	case binary.LittleEndian.Uint32(magic[:]) == magicNano:
		st.order, st.nano = binary.LittleEndian, true
	case binary.BigEndian.Uint32(magic[:]) == magicNano:
		st.order, st.nano = binary.BigEndian, true
	default:
		return ErrFormat
	}
	var rest [pcapGlobalHdrLen - 4]byte
	if err := rd.readFull(rest[:]); err != nil {
		return fmt.Errorf("%w: global header: %w", ErrFormat, err)
	}
	// rest: version 2+2, reserved 4+4, snaplen 4, link type 4 (low 16 bits; the
	// upper bits carry FCS information).
	st.linkType = uint16(st.order.Uint32(rest[16:20]) & 0xffff)
	rd.pcap = st
	return nil
}

func (rd *Reader) nextPcap() (Record, error) {
	start := rd.off
	if err := rd.readFull(rd.hdr[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return Record{}, io.EOF
		}
		return Record{}, fmt.Errorf("pcapfile: record header: %w", err)
	}
	o := rd.pcap.order
	sec, frac := o.Uint32(rd.hdr[0:4]), o.Uint32(rd.hdr[4:8])
	incl, orig := o.Uint32(rd.hdr[8:12]), o.Uint32(rd.hdr[12:16])
	if incl > MaxPacket {
		return Record{}, fmt.Errorf("%w: packet of %d bytes at offset %d", ErrTooLarge, incl, start)
	}
	data := rd.grow(int(incl))
	if err := rd.readFull(data); err != nil {
		return Record{}, fmt.Errorf("pcapfile: packet data: %w", unexpected(err))
	}
	nsec := int64(frac)
	if !rd.pcap.nano {
		nsec *= int64(time.Microsecond)
	}
	if nsec >= int64(time.Second) {
		return Record{}, fmt.Errorf("%w: sub-second field %d at offset %d", ErrCorrupt, frac, start)
	}
	rd.index++
	return Record{
		At: time.Unix(int64(sec), nsec).UTC(), LinkType: rd.pcap.linkType, Data: data,
		OrigLen: int(orig), Offset: start, Index: rd.index,
	}, nil
}

// unexpected turns a plain EOF inside a record into io.ErrUnexpectedEOF.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
