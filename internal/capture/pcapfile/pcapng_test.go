// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pcapfile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"
)

// ngBuilder writes pcapng blocks by hand, from the format description.
type ngBuilder struct {
	o interface {
		binary.ByteOrder
		binary.AppendByteOrder
	}
	b []byte
}

func (g *ngBuilder) block(typ uint32, body []byte) {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	total := uint32(ngBlockMinLen + len(body))
	g.b = g.o.AppendUint32(g.b, typ)
	g.b = g.o.AppendUint32(g.b, total)
	g.b = append(g.b, body...)
	g.b = g.o.AppendUint32(g.b, total)
}

func (g *ngBuilder) shb() {
	body := g.o.AppendUint32(nil, ngByteOrderMagic)
	body = g.o.AppendUint16(body, 1)
	body = g.o.AppendUint16(body, 0)
	body = g.o.AppendUint64(body, ^uint64(0)) // section length unknown
	g.block(ngBlockSHB, body)
}

// idb adds an interface; tsresol < 0 leaves the default (microseconds).
func (g *ngBuilder) idb(link uint16, tsresol int, tsoffset int64) {
	body := g.o.AppendUint16(nil, link)
	body = g.o.AppendUint16(body, 0)
	body = g.o.AppendUint32(body, 0)
	if tsresol >= 0 {
		body = g.o.AppendUint16(body, ngOptTSResol)
		body = g.o.AppendUint16(body, 1)
		body = append(body, byte(tsresol), 0, 0, 0)
	}
	if tsoffset != 0 {
		body = g.o.AppendUint16(body, ngOptTSOffset)
		body = g.o.AppendUint16(body, 8)
		body = g.o.AppendUint64(body, uint64(tsoffset))
	}
	body = g.o.AppendUint32(body, 0) // opt_endofopt
	g.block(ngBlockIDB, body)
}

func (g *ngBuilder) epb(iface uint32, ts uint64, data []byte) {
	body := g.o.AppendUint32(nil, iface)
	body = g.o.AppendUint32(body, uint32(ts>>32))
	body = g.o.AppendUint32(body, uint32(ts))
	body = g.o.AppendUint32(body, uint32(len(data)))
	body = g.o.AppendUint32(body, uint32(len(data)))
	g.block(ngBlockEPB, append(body, data...))
}

func (g *ngBuilder) spb(data []byte) {
	body := g.o.AppendUint32(nil, uint32(len(data)))
	g.block(ngBlockSPB, append(body, data...))
}

// samplePcapng: an LE section with two interfaces (default microseconds and
// nanoseconds with an offset), an unknown block, a simple packet block, then
// a big-endian section.
func samplePcapng() []byte {
	g := &ngBuilder{o: binary.LittleEndian}
	g.shb()
	g.idb(linkRaw, -1, 0)
	g.idb(1, 9, 10)
	g.epb(0, 1_700_000_000_250_000, []byte{1, 2, 3})
	g.block(0x00000004, []byte("name resolution block is skipped"))
	g.epb(1, 1_700_000_000_000_000_007, []byte{4, 5, 6, 7, 8})
	g.spb([]byte{9, 9})
	le := g.b
	g2 := &ngBuilder{o: binary.BigEndian}
	g2.shb()
	g2.idb(linkRaw, 0x80|10, 0) // 2^-10 s
	g2.epb(0, 1024*3+512, []byte{0xaa})
	return append(le, g2.b...)
}

func TestReadPcapng(t *testing.T) {
	recs := readAll(t, samplePcapng())
	want := []struct {
		at   time.Time
		link uint16
		data []byte
	}{
		{time.Unix(1_700_000_000, 250_000_000), linkRaw, []byte{1, 2, 3}},
		{time.Unix(1_700_000_010, 7), 1, []byte{4, 5, 6, 7, 8}},
		{time.Unix(1_700_000_010, 7), linkRaw, []byte{9, 9}}, // SPB: interface 0, time of the previous packet
		{time.Unix(3, 500_000_000), linkRaw, []byte{0xaa}},
	}
	if len(recs) != len(want) {
		t.Fatalf("got %d records, want %d", len(recs), len(want))
	}
	for i, w := range want {
		r := recs[i]
		if !r.At.Equal(w.at) || r.LinkType != w.link || !bytes.Equal(r.Data, w.data) || r.Index != i+1 {
			t.Fatalf("record %d = at %s link %d data %x index %d; want at %s link %d data %x",
				i, r.At, r.LinkType, r.Data, r.Index, w.at, w.link, w.data)
		}
	}
	if recs[0].Offset == 0 || recs[1].Offset <= recs[0].Offset {
		t.Fatalf("offsets not increasing: %d, %d", recs[0].Offset, recs[1].Offset)
	}
}

func TestReadPcapngOffsetPointsAtBlock(t *testing.T) {
	b := samplePcapng()
	recs := readAll(t, b)
	for _, r := range recs[:2] {
		if typ := binary.LittleEndian.Uint32(b[r.Offset:]); typ != ngBlockEPB {
			t.Fatalf("offset %d holds block type %#x, want EPB", r.Offset, typ)
		}
	}
}

func TestReadPcapngRejects(t *testing.T) {
	good := samplePcapng()
	tests := []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{"bad trailer", func(b []byte) []byte {
			b[len(b)-1] ^= 0xff
			return b
		}, ErrCorrupt},
		{"packet for a missing interface", func([]byte) []byte {
			g := &ngBuilder{o: binary.LittleEndian}
			g.shb()
			g.epb(3, 0, []byte{1})
			return g.b
		}, ErrCorrupt},
		{"truncated block", func(b []byte) []byte { return b[:len(b)-3] }, io.ErrUnexpectedEOF},
		{"bad tsresol", func([]byte) []byte {
			g := &ngBuilder{o: binary.LittleEndian}
			g.shb()
			g.idb(linkRaw, 30, 0)
			return g.b
		}, ErrCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.mutate(bytes.Clone(good))
			rd, err := NewReader(bytes.NewReader(in))
			if err != nil {
				t.Fatal(err)
			}
			for err == nil {
				_, err = rd.Next()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadPcapngBadByteOrder(t *testing.T) {
	b := samplePcapng()
	b[8] ^= 0xff // byte-order magic
	if _, err := NewReader(bytes.NewReader(b)); !errors.Is(err, ErrFormat) {
		t.Fatalf("err = %v, want ErrFormat", err)
	}
}

// FuzzReader feeds arbitrary bytes to the reader: it must never panic,
// never return data over MaxPacket and always terminate.
func FuzzReader(f *testing.F) {
	f.Add(samplePcapng())
	f.Add(bigEndianMicroPcap())
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, linkRaw)
	_, _ = w.WritePacket(t0, []byte{0x45, 0, 0, 20})
	f.Add(buf.Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		rd, err := NewReader(bytes.NewReader(b))
		if err != nil {
			return
		}
		for range len(b) + 1 { // every record consumes at least one byte
			rec, err := rd.Next()
			if err != nil {
				return
			}
			if len(rec.Data) > MaxPacket {
				t.Fatalf("record of %d bytes", len(rec.Data))
			}
		}
		t.Fatal("reader did not terminate")
	})
}
