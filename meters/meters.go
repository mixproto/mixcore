// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package meters carries meter levels from a driver to the apps. Meters are
// display-only and "latest wins": a Slot keeps only the newest Frame, frames
// are dropped and never queued, and no step allocates.
package meters

import (
	"encoding/binary"
	"math"
	"sync"
	"time"
)

// MaxMeterChannels is the largest number of levels one Frame carries.
const MaxMeterChannels = 128

// Frame is one set of meter levels in dBFS. Only Levels[:Count] are valid; a
// level of -inf means no signal.
type Frame struct {
	Seq    uint64
	At     time.Time
	Count  int
	Levels [MaxMeterChannels]float32
}

// Slot keeps the newest Frame. Publish and CopyLatest copy a fixed-size array
// under a mutex that is never held across I/O, so both return in bounded time.
type Slot struct {
	mu    sync.Mutex
	frame Frame
}

// Publish stores a copy of f as the newest frame.
func (s *Slot) Publish(f *Frame) {
	s.mu.Lock()
	s.frame = *f
	s.mu.Unlock()
}

// CopyLatest copies the newest frame into dst; it returns false when dst
// already holds it (same Seq) or nothing was published yet.
func (s *Slot) CopyLatest(dst *Frame) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frame.Seq == dst.Seq {
		return false
	}
	*dst = s.frame
	return true
}

// Reset forgets the newest frame (used when meters stop or the console is lost).
func (s *Slot) Reset() {
	s.mu.Lock()
	s.frame = Frame{}
	s.mu.Unlock()
}

// Binary frame layout v0 (contracts/core-api/meter-frame.md), all little-endian:
//
//	0  [4]byte  magic "MXMF"
//	4  uint16   version (0)
//	6  uint16   count n
//	8  uint64   seq
//	16 int64    capture time, Unix nanoseconds
//	24 n x float32 levels in dBFS (-inf = no signal)
const (
	FrameMagic      = "MXMF"
	FrameVersion    = 0
	FrameHeaderSize = 24
	levelSize       = 4
)

// MaxEncodedSize is the largest v0 binary frame: a buffer of this size always fits.
const MaxEncodedSize = FrameHeaderSize + levelSize*MaxMeterChannels

func levelCount(f *Frame) int { return min(max(f.Count, 0), MaxMeterChannels) }

// EncodedSize is the length of the v0 binary encoding of f.
func EncodedSize(f *Frame) int { return FrameHeaderSize + levelSize*levelCount(f) }

// AppendBinary appends the v0 binary encoding of f to dst.
func AppendBinary(dst []byte, f *Frame) []byte {
	n := levelCount(f)
	dst = append(dst, FrameMagic...)
	dst = binary.LittleEndian.AppendUint16(dst, FrameVersion)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(n))
	dst = binary.LittleEndian.AppendUint64(dst, f.Seq)
	var at int64
	if !f.At.IsZero() {
		at = f.At.UnixNano()
	}
	dst = binary.LittleEndian.AppendUint64(dst, uint64(at))
	for _, l := range f.Levels[:n] {
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(l))
	}
	return dst
}

// LinearToDBFS converts a linear amplitude (1.0 = 0 dBFS) to dBFS. Zero,
// negative and NaN inputs give -inf: a broken packet must show silence, not a
// full-scale meter.
func LinearToDBFS(v float32) float32 {
	if !(v > 0) || math.IsInf(float64(v), 1) {
		return float32(math.Inf(-1))
	}
	return float32(20 * math.Log10(float64(v)))
}
