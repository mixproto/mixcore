// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// Binary limits and scores.
const (
	scoreBinary     = 1 // the fallback: always applicable, always last
	maxFrame        = 65536
	minGuessFrames  = 3
	binaryValueShow = 256
	maxBinaryBuffer = 1 << 20
)

// Framing is a guessed length-prefix framing of a TCP stream: a length field
// of Width bytes at Offset, counting either the bytes after the field or the
// whole frame. The zero Framing means "one frame per chunk".
type Framing struct {
	Offset, Width int
	Little        bool
	Inclusive     bool // the length counts the whole frame, header included
}

// String describes the framing for reports.
func (f Framing) String() string {
	if f.Width == 0 {
		return "none (one frame per chunk)"
	}
	order := "be"
	if f.Little {
		order = "le"
	}
	if f.Width == 1 {
		order = ""
	}
	counts := "bytes after the field"
	if f.Inclusive {
		counts = "whole frame"
	}
	return fmt.Sprintf("u%d%s@%d (%s)", f.Width*8, order, f.Offset, counts)
}

// frameLen returns the length of the frame starting at b[0]; ok=false when b
// is too short to know, err=true when the length is impossible.
func (f Framing) frameLen(b []byte) (n int, ok, bad bool) {
	end := f.Offset + f.Width
	if len(b) < end {
		return 0, false, false
	}
	var l uint64
	field := b[f.Offset:end]
	switch {
	case f.Width == 1:
		l = uint64(field[0])
	case f.Width == 2 && f.Little:
		l = uint64(binary.LittleEndian.Uint16(field))
	case f.Width == 2:
		l = uint64(binary.BigEndian.Uint16(field))
	case f.Little:
		l = uint64(binary.LittleEndian.Uint32(field))
	default:
		l = uint64(binary.BigEndian.Uint32(field))
	}
	if l == 0 {
		return 0, false, true // an empty length field is not a framing
	}
	if !f.Inclusive {
		l += uint64(end)
	}
	if l < uint64(end) || l > maxFrame {
		return 0, false, true
	}
	return int(l), true, false
}

// candidates are the framings GuessFraming tries, in preference order.
func candidates() []Framing {
	var out []Framing
	for _, off := range []int{0, 2, 4} {
		for _, w := range []int{2, 4, 1} {
			for _, little := range []bool{false, true} {
				if w == 1 && little {
					continue
				}
				for _, incl := range []bool{false, true} {
					out = append(out, Framing{Offset: off, Width: w, Little: little, Inclusive: incl})
				}
			}
		}
	}
	return out
}

// GuessFraming returns the first candidate (in preference order) that tiles
// sample into at least minGuessFrames frames with nothing left over except
// one unfinished frame. ok=false when none does.
func GuessFraming(sample []byte) (Framing, bool) {
	for _, c := range candidates() {
		frames, pos := 0, 0
		for pos < len(sample) {
			n, ok, bad := c.frameLen(sample[pos:])
			if bad {
				frames = 0
				break
			}
			if !ok || pos+n > len(sample) {
				break // an unfinished last frame is fine
			}
			pos += n
			frames++
		}
		if frames >= minGuessFrames {
			return c, true
		}
	}
	return Framing{}, false
}

// Binary is the fallback decoder. A frame is a UDP datagram, a guessed
// length-prefixed TCP frame, or (no framing found) a TCP chunk. Key: the
// first BinaryKeyBytes bytes in hex plus the frame length, e.g.
// "a5010017/12"; value: the remaining bytes in hex. The analyzer reports
// which byte offsets change for each key.
type Binary struct{ opts Options }

// Name implements Decoder.
func (Binary) Name() string { return "binary" }

// Detect implements Decoder.
func (Binary) Detect(netpkt.Transport, [][]byte) int { return scoreBinary }

// NewStream implements Decoder.
func (d Binary) NewStream(t netpkt.Transport, sample []byte) Stream {
	s := &binaryStream{keyBytes: d.opts.withDefaults().BinaryKeyBytes, buf: buffer{max: maxBinaryBuffer}}
	if t == netpkt.TCP {
		s.framing, s.framed = GuessFraming(sample)
	}
	return s
}

// FramingOf reports the framing a binary stream uses (for reports).
func FramingOf(s Stream) (Framing, bool) {
	b, ok := s.(*binaryStream)
	if !ok {
		return Framing{}, false
	}
	return b.framing, b.framed
}

type binaryStream struct {
	keyBytes int
	framing  Framing
	framed   bool
	buf      buffer
}

func (s *binaryStream) Feed(chunk []byte, emit func(Message)) {
	if !s.framed {
		emitBinary(chunk, s.keyBytes, emit)
		return
	}
	if !s.buf.add(chunk) {
		return
	}
	used := 0
	for used < len(s.buf.b) {
		n, ok, bad := s.framing.frameLen(s.buf.b[used:])
		if bad {
			emitBinary(s.buf.b[used:], s.keyBytes, emit) // lost sync: show the rest as one frame
			used = len(s.buf.b)
			break
		}
		if !ok || used+n > len(s.buf.b) {
			break
		}
		emitBinary(s.buf.b[used:used+n], s.keyBytes, emit)
		used += n
	}
	s.buf.consume(used)
}

func emitBinary(frame []byte, keyBytes int, emit func(Message)) {
	if len(frame) == 0 {
		return
	}
	k := min(keyBytes, len(frame))
	key := Key{KindBinary, fmt.Sprintf("%s/%d", hex.EncodeToString(frame[:k]), len(frame))}
	emit(newMessage(key, TextValue(hexCap(frame[k:], binaryValueShow)), frame))
}
