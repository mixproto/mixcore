// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pcapfile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"time"
)

// ngState is the pcapng section being read.
type ngState struct {
	order  binary.ByteOrder
	ifaces []ngIface
	lastAt time.Time // Simple Packet Blocks carry no timestamp
}

type ngIface struct {
	linkType uint16
	snapLen  uint32
	ups      uint64 // timestamp units per second
	offset   int64  // seconds added to every timestamp (if_tsoffset)
}

// readSHBAfterType reads a Section Header Block whose type was consumed.
func (rd *Reader) readSHBAfterType() error {
	var head [8]byte // block total length, byte-order magic
	if err := rd.readFull(head[:]); err != nil {
		return fmt.Errorf("%w: section header: %w", ErrFormat, unexpected(err))
	}
	var order binary.ByteOrder
	switch {
	case binary.LittleEndian.Uint32(head[4:]) == ngByteOrderMagic:
		order = binary.LittleEndian
	case binary.BigEndian.Uint32(head[4:]) == ngByteOrderMagic:
		order = binary.BigEndian
	default:
		return fmt.Errorf("%w: pcapng byte-order magic", ErrFormat)
	}
	total := order.Uint32(head[:4])
	const shbMin = ngBlockMinLen + 16 // magic, version, section length
	if total < shbMin || total%4 != 0 || total > maxSkipBlock {
		return fmt.Errorf("%w: section header length %d", ErrCorrupt, total)
	}
	// Read so far: type, length, byte-order magic. The rest of the body
	// (version, section length, options) is not needed; the trailer is checked.
	const readSoFar, trailer = 12, 4
	if err := rd.skip(int64(total) - readSoFar - trailer); err != nil {
		return err
	}
	if err := rd.checkTrailer(order, total); err != nil {
		return err
	}
	rd.ng = ngState{order: order}
	return nil
}

func (rd *Reader) skip(n int64) error {
	got, err := io.CopyN(io.Discard, rd.r, n)
	rd.off += got
	if err != nil {
		return fmt.Errorf("pcapfile: block body: %w", unexpected(err))
	}
	return nil
}

func (rd *Reader) checkTrailer(order binary.ByteOrder, total uint32) error {
	var tl [4]byte
	if err := rd.readFull(tl[:]); err != nil {
		return fmt.Errorf("pcapfile: block trailer: %w", unexpected(err))
	}
	if order.Uint32(tl[:]) != total {
		return fmt.Errorf("%w: block trailer length differs from header", ErrCorrupt)
	}
	return nil
}

func (rd *Reader) nextNG() (Record, error) {
	for {
		start := rd.off
		var head [8]byte
		if err := rd.readFull(head[:4]); err != nil {
			if errors.Is(err, io.EOF) {
				return Record{}, io.EOF
			}
			return Record{}, fmt.Errorf("pcapfile: block type: %w", unexpected(err))
		}
		typ := rd.ng.order.Uint32(head[:4])
		if typ == ngBlockSHB {
			if err := rd.readSHBAfterType(); err != nil {
				return Record{}, err
			}
			continue
		}
		if err := rd.readFull(head[4:]); err != nil {
			return Record{}, fmt.Errorf("pcapfile: block length: %w", unexpected(err))
		}
		rec, ok, err := rd.block(typ, rd.ng.order.Uint32(head[4:]), start)
		if err != nil || ok {
			return rec, err
		}
	}
}

// block reads one non-SHB block; ok is true when it held a packet.
func (rd *Reader) block(typ, total uint32, start int64) (Record, bool, error) {
	if total < ngBlockMinLen || total%4 != 0 {
		return Record{}, false, fmt.Errorf("%w: block length %d at offset %d", ErrCorrupt, total, start)
	}
	bodyLen := int64(total) - ngBlockMinLen
	switch typ {
	case ngBlockIDB, ngBlockEPB, ngBlockSPB, ngBlockOPB:
	default:
		if bodyLen > maxSkipBlock {
			return Record{}, false, fmt.Errorf("%w: block of %d bytes", ErrTooLarge, total)
		}
		if err := rd.skip(bodyLen); err != nil {
			return Record{}, false, err
		}
		return Record{}, false, rd.checkTrailer(rd.ng.order, total)
	}
	if bodyLen > MaxPacket+maxOptionBytes {
		return Record{}, false, fmt.Errorf("%w: block of %d bytes", ErrTooLarge, total)
	}
	body := rd.grow(int(bodyLen))
	if err := rd.readFull(body); err != nil {
		return Record{}, false, fmt.Errorf("pcapfile: block body: %w", unexpected(err))
	}
	if err := rd.checkTrailer(rd.ng.order, total); err != nil {
		return Record{}, false, err
	}
	if typ == ngBlockIDB {
		return Record{}, false, rd.addInterface(body)
	}
	rec, err := rd.packet(typ, body)
	rec.Offset = start
	return rec, err == nil, err
}

// maxOptionBytes bounds the options that may follow packet data in a block.
const maxOptionBytes = 64 << 10

func (rd *Reader) addInterface(body []byte) error {
	const idbFixed = 8
	if len(body) < idbFixed {
		return fmt.Errorf("%w: short interface block", ErrCorrupt)
	}
	if len(rd.ng.ifaces) >= maxInterfaces {
		return fmt.Errorf("%w: more than %d interfaces", ErrTooLarge, maxInterfaces)
	}
	o := rd.ng.order
	ifc := ngIface{linkType: o.Uint16(body[0:2]), snapLen: o.Uint32(body[4:8])}
	ups, offset, err := parseIDBOptions(o, body[idbFixed:])
	if err != nil {
		return err
	}
	ifc.ups, ifc.offset = ups, offset
	rd.ng.ifaces = append(rd.ng.ifaces, ifc)
	return nil
}

// parseIDBOptions returns the timestamp resolution (units per second) and offset.
func parseIDBOptions(o binary.ByteOrder, opts []byte) (uint64, int64, error) {
	ups, err := tsUnits(defaultTSResolExp)
	if err != nil {
		return 0, 0, err
	}
	var offset int64
	for len(opts) >= 4 {
		code, n := o.Uint16(opts[0:2]), int(o.Uint16(opts[2:4]))
		padded := n + (4-n%4)%4
		if 4+padded > len(opts) {
			return 0, 0, fmt.Errorf("%w: interface option overruns block", ErrCorrupt)
		}
		val := opts[4 : 4+n]
		switch {
		case code == ngOptEnd:
			return ups, offset, nil
		case code == ngOptTSResol && n == 1:
			if ups, err = tsUnits(val[0]); err != nil {
				return 0, 0, err
			}
		case code == ngOptTSOffset && n == 8:
			offset = int64(o.Uint64(val))
		}
		opts = opts[4+padded:]
	}
	return ups, offset, nil
}

// tsUnits decodes if_tsresol: high bit clear = 10^-v seconds, set = 2^-v.
func tsUnits(v byte) (uint64, error) {
	const pow2Flag = 0x80
	const maxPow10 = 19 // 10^19 is the largest power of ten in a uint64
	const maxPow2 = 63
	exp := int(v &^ pow2Flag)
	if v&pow2Flag != 0 {
		if exp > maxPow2 {
			return 0, fmt.Errorf("%w: timestamp resolution 2^-%d", ErrCorrupt, exp)
		}
		return 1 << exp, nil
	}
	if exp > maxPow10 {
		return 0, fmt.Errorf("%w: timestamp resolution 10^-%d", ErrCorrupt, exp)
	}
	u := uint64(1)
	for range exp {
		u *= 10
	}
	return u, nil
}

func (rd *Reader) packet(typ uint32, body []byte) (Record, error) {
	o := rd.ng.order
	var ifID, hi, lo, capLen, origLen uint32
	var data []byte
	switch typ {
	case ngBlockEPB:
		const fixed = 20
		if len(body) < fixed {
			return Record{}, fmt.Errorf("%w: short packet block", ErrCorrupt)
		}
		ifID, hi, lo = o.Uint32(body[0:4]), o.Uint32(body[4:8]), o.Uint32(body[8:12])
		capLen, origLen, data = o.Uint32(body[12:16]), o.Uint32(body[16:20]), body[fixed:]
	case ngBlockOPB:
		const fixed = 20
		if len(body) < fixed {
			return Record{}, fmt.Errorf("%w: short packet block", ErrCorrupt)
		}
		ifID, hi, lo = uint32(o.Uint16(body[0:2])), o.Uint32(body[4:8]), o.Uint32(body[8:12])
		capLen, origLen, data = o.Uint32(body[12:16]), o.Uint32(body[16:20]), body[fixed:]
	default: // ngBlockSPB
		return rd.simplePacket(body)
	}
	if ifID >= uint32(len(rd.ng.ifaces)) { // uint32: int(ifID) is negative on 32-bit for ifID >= 2^31
		return Record{}, fmt.Errorf("%w: packet for unknown interface %d", ErrCorrupt, ifID)
	}
	if capLen > MaxPacket || int(capLen) > len(data) {
		return Record{}, fmt.Errorf("%w: captured length %d", ErrCorrupt, capLen)
	}
	ifc := rd.ng.ifaces[ifID]
	at := ngTime(uint64(hi)<<32|uint64(lo), ifc)
	rd.ng.lastAt = at
	rd.index++
	return Record{At: at, LinkType: ifc.linkType, Data: data[:capLen], OrigLen: int(origLen), Index: rd.index}, nil
}

func (rd *Reader) simplePacket(body []byte) (Record, error) {
	if len(rd.ng.ifaces) == 0 || len(body) < 4 {
		return Record{}, fmt.Errorf("%w: simple packet block without interface", ErrCorrupt)
	}
	ifc := rd.ng.ifaces[0]
	orig := rd.ng.order.Uint32(body[0:4])
	n := min(uint64(orig), uint64(len(body)-4), MaxPacket)
	if ifc.snapLen > 0 {
		n = min(n, uint64(ifc.snapLen))
	}
	rd.index++
	return Record{At: rd.ng.lastAt, LinkType: ifc.linkType, Data: body[4 : 4+n], OrigLen: int(orig), Index: rd.index}, nil
}

// ngTime converts a pcapng timestamp in ifc's units to UTC time.
func ngTime(ts uint64, ifc ngIface) time.Time {
	sec := ts / ifc.ups
	frac := ts % ifc.ups
	hi, lo := bits.Mul64(frac, uint64(time.Second))
	nsec, _ := bits.Div64(hi, lo, ifc.ups) // hi < ups because frac < ups
	return time.Unix(int64(sec)+ifc.offset, int64(nsec)).UTC()
}
