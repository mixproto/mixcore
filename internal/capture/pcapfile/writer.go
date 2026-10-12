// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pcapfile

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// pcap file format version written by Writer.
const (
	versionMajor = 2
	versionMinor = 4
)

// Writer writes a classic pcap file with nanosecond timestamps, little endian.
type Writer struct {
	w   io.Writer
	off int64
	hdr [pcapRecordHdrLen]byte
}

// NewWriter writes the global header for linkType and returns the writer.
func NewWriter(w io.Writer, linkType uint16) (*Writer, error) {
	var h [pcapGlobalHdrLen]byte
	le := binary.LittleEndian
	le.PutUint32(h[0:4], magicNano)
	le.PutUint16(h[4:6], versionMajor)
	le.PutUint16(h[6:8], versionMinor)
	le.PutUint32(h[16:20], MaxPacket)
	le.PutUint32(h[20:24], uint32(linkType))
	if _, err := w.Write(h[:]); err != nil {
		return nil, fmt.Errorf("pcapfile: write header: %w", err)
	}
	return &Writer{w: w, off: pcapGlobalHdrLen}, nil
}

// WritePacket appends one packet and returns the byte offset of its record,
// the same offset Reader reports for it.
func (w *Writer) WritePacket(at time.Time, data []byte) (int64, error) {
	if len(data) > MaxPacket {
		return 0, fmt.Errorf("%w: packet of %d bytes", ErrTooLarge, len(data))
	}
	le := binary.LittleEndian
	sec := at.Unix()
	if sec < 0 || sec > int64(^uint32(0)) {
		return 0, fmt.Errorf("pcapfile: time %s outside the pcap range", at)
	}
	le.PutUint32(w.hdr[0:4], uint32(sec))
	le.PutUint32(w.hdr[4:8], uint32(at.Nanosecond()))
	le.PutUint32(w.hdr[8:12], uint32(len(data)))
	le.PutUint32(w.hdr[12:16], uint32(len(data)))
	off := w.off
	if _, err := w.w.Write(w.hdr[:]); err != nil {
		return 0, fmt.Errorf("pcapfile: write record: %w", err)
	}
	if _, err := w.w.Write(data); err != nil {
		return 0, fmt.Errorf("pcapfile: write record: %w", err)
	}
	w.off += pcapRecordHdrLen + int64(len(data))
	return off, nil
}
