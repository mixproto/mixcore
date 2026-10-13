// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

// errBadMeters marks a meter packet that does not match XA-F51/XA-F53.
var errBadMeters = errors.New("xair: malformed meter packet")

const (
	meterCountSize = 4 // little-endian int32 count (XA-F51)
	meterValueSize = 2 // little-endian int16 per value, 1/256 dB (XA-F51)
	// bank1MainL is the value index of Main L in bank 1; Main R follows (XA-F53).
	bank1MainL = 36
	// dbPerStep: values are in 1/256 dB (XA-F51, XA-F52).
	dbPerStep = 1.0 / 256
	// silence is -128 dB, no signal (XA-F52); mixcore maps it to -inf.
	silence = math.MinInt16
)

// Meter frame layout (contracts/core-api/meter-frame.md): inputs 1..16,
// then (core-api v0.5) buses 1..6 and FX sends 1..4 (buses 7..10), then
// Main L and Main R.
const (
	busMeters     = 10                        // XA-F53: 6 buses + 4 FX sends, bank 1 values 26..35
	bank1Bus1     = 26                        // XA-F53: value index of bus 1 in bank 1
	busMeterLevel = inputChannels             // level index of bus 1
	mainLLevel    = busMeterLevel + busMeters // level index of Main L; Main R follows
	meterLevels   = mainLLevel + 2            // levels in a frame
)

var (
	// meterPrefix is the padded address of a bank 1 packet.
	meterPrefix = []byte(meterBank1 + "\x00\x00\x00")
	// meterRequests: /meters ,si "/meters/1" 0, the vendor-documented shape
	// (XA-F50) and the only one mixcore sends (XA-F57, XA-F76).
	meterRequests = [][]osc.Arg{{osc.String(meterBank1), osc.Int(0)}}
)

// meterCodec implements xosc.MeterCodec for bank 1.
type meterCodec struct{}

func (meterCodec) Levels() int           { return meterLevels }
func (meterCodec) BusMeters() (int, int) { return busMeterLevel, busMeters }
func (meterCodec) Requests() [][]osc.Arg { return meterRequests }

func (meterCodec) Bank(pkt []byte) (int, bool) { return 1, bytes.HasPrefix(pkt, meterPrefix) }

func (meterCodec) Decode(_ int, blob []byte, fr *meters.Frame) error { return decodeBank1(blob, fr) }

// decodeBank1 reads a bank 1 blob (XA-F51, XA-F53): a little-endian int32
// count, then count little-endian int16 values in 1/256 dB. Values 0..15 are
// inputs 1..16 (pre-fader), 26..35 buses 1..6 and FX sends 1..4, 36 and 37
// Main L/R (post-fader). The blob must
// hold Main R, and the count must not exceed the blob; anything else is
// rejected, never guessed.
func decodeBank1(blob []byte, fr *meters.Frame) error {
	if len(blob) < meterCountSize {
		return errBadMeters
	}
	count := binary.LittleEndian.Uint32(blob)
	avail := uint32((len(blob) - meterCountSize) / meterValueSize)
	if count > avail || count < bank1MainL+2 {
		return errBadMeters
	}
	for i := range inputChannels {
		fr.Levels[i] = dbfsFromWire(value(blob, i))
	}
	for b := range busMeters { // pre bus master (XA-F53 "all pre")
		fr.Levels[busMeterLevel+b] = dbfsFromWire(value(blob, bank1Bus1+b))
	}
	fr.Levels[mainLLevel] = dbfsFromWire(value(blob, bank1MainL))
	fr.Levels[mainLLevel+1] = dbfsFromWire(value(blob, bank1MainL+1))
	return nil
}

func value(blob []byte, i int) int16 {
	return int16(binary.LittleEndian.Uint16(blob[meterCountSize+i*meterValueSize:]))
}

// dbfsFromWire converts one meter value to dBFS: v/256, and -32768 (-128 dB,
// no signal) to -inf (XA-F52).
func dbfsFromWire(v int16) float32 {
	if v == silence {
		return float32(math.Inf(-1))
	}
	return float32(float64(v) * dbPerStep)
}
