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

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const linkRaw = 101

var t0 = time.Date(2026, 10, 12, 14, 3, 5, 123456789, time.UTC)

func readAll(t *testing.T, b []byte) []Record {
	t.Helper()
	rd, err := NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	var out []Record
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rec.Data = bytes.Clone(rec.Data)
		out = append(out, rec)
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, linkRaw)
	if err != nil {
		t.Fatal(err)
	}
	offs := make([]int64, 0, 3)
	for i, p := range [][]byte{{1, 2, 3}, {}, bytes.Repeat([]byte{9}, 1500)} {
		off, err := w.WritePacket(t0.Add(time.Duration(i)*time.Millisecond), p)
		if err != nil {
			t.Fatal(err)
		}
		offs = append(offs, off)
	}
	recs := readAll(t, buf.Bytes())
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	for i, r := range recs {
		if r.Offset != offs[i] || r.Index != i+1 || r.LinkType != linkRaw {
			t.Fatalf("record %d: offset %d index %d link %d, want offset %d", i, r.Offset, r.Index, r.LinkType, offs[i])
		}
		if want := t0.Add(time.Duration(i) * time.Millisecond); !r.At.Equal(want) {
			t.Fatalf("record %d at %s, want %s (nanoseconds kept)", i, r.At, want)
		}
	}
	if len(recs[2].Data) != 1500 || offs[0] != pcapGlobalHdrLen {
		t.Fatalf("data length %d, first offset %d", len(recs[2].Data), offs[0])
	}
}

func TestWriterRejectsOversizedPacket(t *testing.T) {
	w, err := NewWriter(io.Discard, linkRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WritePacket(t0, make([]byte, MaxPacket+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// bigEndianMicroPcap is a classic big-endian microsecond file with one packet.
func bigEndianMicroPcap() []byte {
	be := binary.BigEndian
	b := be.AppendUint32(nil, magicMicro)
	b = be.AppendUint16(b, 2)
	b = be.AppendUint16(b, 4)
	b = append(b, make([]byte, 8)...)
	b = be.AppendUint32(b, 65535)
	b = be.AppendUint32(b, 1) // Ethernet
	b = be.AppendUint32(b, 1_700_000_000)
	b = be.AppendUint32(b, 250_000) // microseconds
	b = be.AppendUint32(b, 4)
	b = be.AppendUint32(b, 60)
	return append(b, 0xde, 0xad, 0xbe, 0xef)
}

func TestReadBigEndianMicroseconds(t *testing.T) {
	recs := readAll(t, bigEndianMicroPcap())
	if len(recs) != 1 {
		t.Fatalf("got %d records", len(recs))
	}
	r := recs[0]
	want := time.Unix(1_700_000_000, 250_000_000).UTC()
	if !r.At.Equal(want) || r.LinkType != 1 || r.OrigLen != 60 || !bytes.Equal(r.Data, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("record = %+v", r)
	}
}

func TestReadRejects(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"not a capture", []byte("hello world, not pcap at all"), ErrFormat},
		{"empty", nil, ErrFormat},
		{"truncated global header", bigEndianMicroPcap()[:10], ErrFormat},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewReader(bytes.NewReader(tc.in)); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadTruncatedRecordIsUnexpectedEOF(t *testing.T) {
	b := bigEndianMicroPcap()
	rd, err := NewReader(bytes.NewReader(b[:len(b)-2]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadOversizedRecord(t *testing.T) {
	b := bigEndianMicroPcap()
	binary.BigEndian.PutUint32(b[pcapGlobalHdrLen+8:], MaxPacket+1)
	rd, err := NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.Next(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
