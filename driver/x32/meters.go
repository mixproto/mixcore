// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

// errBadMeters marks a meter packet that does not match X32-F53.
var errBadMeters = errors.New("x32: malformed meter packet")

const (
	meterCountSize = 4 // little-endian int32 count (X32-F53)
	meterValueSize = 4 // little-endian float32 per value (X32-F53)
)

// Meter frame layout (contracts/core-api/meter-frame.md): inputs 1..32, then
// Main L and Main R.
const (
	mainLLevel  = inputChannels     // level index of Main L; Main R follows
	meterLevels = inputChannels + 2 // levels in a frame
	bank2MainL  = 22                // X32-F55: value index of Main L in bank 2; Main R follows
)

// Meter banks as codec bank numbers.
const (
	bankInputs = iota + 1 // bank 1
	bankBuses             // bank 2
)

// Padded addresses of bank 1 and bank 2 packets; they let the reader
// recognise meter packets before decoding them.
var (
	meterPrefix1 = []byte(meterBank1 + "\x00\x00\x00")
	meterPrefix2 = []byte(meterBank2 + "\x00\x00\x00")
	// meterRequests start bank 1 (inputs) and bank 2 (Main L/R) (X32-F50,
	// X32-F52, X32-F55).
	meterRequests = [][]osc.Arg{{osc.String(meterBank1)}, {osc.String(meterBank2)}}
)

// meterCodec implements xosc.MeterCodec for banks 1 and 2.
type meterCodec struct{}

func (meterCodec) Levels() int           { return meterLevels }
func (meterCodec) Requests() [][]osc.Arg { return meterRequests }

func (meterCodec) Bank(pkt []byte) (int, bool) {
	switch {
	case bytes.HasPrefix(pkt, meterPrefix1):
		return bankInputs, true
	case bytes.HasPrefix(pkt, meterPrefix2):
		return bankBuses, true
	default:
		return 0, false
	}
}

// Decode updates the inputs (bank 1) or Main L/R (bank 2) of the frame.
func (meterCodec) Decode(bank int, blob []byte, fr *meters.Frame) error {
	if bank != bankInputs {
		return decodeMainLR(blob, fr)
	}
	var in meters.Frame // stack copy keeps Main L/R of fr; no allocation
	if err := decodeMeters1(blob, &in); err != nil {
		return err
	}
	copy(fr.Levels[:in.Count], in.Levels[:in.Count])
	for i := in.Count; i < inputChannels; i++ {
		fr.Levels[i] = float32(math.Inf(-1))
	}
	return nil
}

// decodeMainLR reads Main L/R of a bank 2 blob (X32-F53, X32-F55) into
// fr.Levels[mainLLevel] and fr.Levels[mainLLevel+1].
func decodeMainLR(blob []byte, fr *meters.Frame) error {
	if len(blob) < meterCountSize {
		return errBadMeters
	}
	count := binary.LittleEndian.Uint32(blob)
	avail := uint32((len(blob) - meterCountSize) / meterValueSize)
	if count > avail || count < bank2MainL+2 {
		return errBadMeters
	}
	for i := range 2 {
		off := meterCountSize + (bank2MainL+i)*meterValueSize
		fr.Levels[mainLLevel+i] = meters.LinearToDBFS(math.Float32frombits(binary.LittleEndian.Uint32(blob[off:])))
	}
	return nil
}

// decodeMeters1 decodes a bank 1 blob (X32-F51, X32-F53, X32-F54) into fr:
// the first 32 values are input channels 1..32, converted to dBFS.
func decodeMeters1(blob []byte, fr *meters.Frame) error {
	if len(blob) < meterCountSize {
		return errBadMeters
	}
	count := binary.LittleEndian.Uint32(blob)
	avail := uint32((len(blob) - meterCountSize) / meterValueSize)
	if count > avail {
		return errBadMeters
	}
	n := int(min(count, inputChannels))
	for i := range n {
		off := meterCountSize + i*meterValueSize
		v := math.Float32frombits(binary.LittleEndian.Uint32(blob[off:]))
		fr.Levels[i] = meters.LinearToDBFS(v)
	}
	fr.Count = n
	return nil
}
