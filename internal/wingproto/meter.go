// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"encoding/binary"
	"errors"
	"math"
)

// Meter block types of a request (WG-F50).
const (
	MeterChannel byte = 0xA0 // channel 1..40
	MeterAux     byte = 0xA1 // aux 1..8
	MeterBus     byte = 0xA2 // bus 1..16
	MeterMain    byte = 0xA3 // main 1..4
	MeterMatrix  byte = 0xA4 // matrix 1..8
	MeterDCA     byte = 0xA5 // dca 1..8 (WG-F55)
)

// Meter packet facts (WG-F52, WG-F53).
const (
	// MeterIDBytes is the report id at the start of every packet.
	MeterIDBytes = 4
	// MeterWordsPerStrip: input L, input R, output L, output R, gate key,
	// gate gain, dyn key, dyn gain (channel, aux, bus, main, matrix).
	MeterWordsPerStrip = 8
	// Word offsets inside a strip block.
	MeterInL  = 0
	MeterInR  = 1
	MeterOutL = 2
	MeterOutR = 3
	// meterDBScale: levels are int16 in 1/256 dB.
	meterDBScale = 256
	// meterIndexMax is the highest index byte (00..7F = 1..128).
	meterIndexMax = 0x7F
)

// ErrMeterRequest means a meter block cannot be encoded.
var ErrMeterRequest = errors.New("wingproto: meter block out of range")

// MeterBlock is one requested meter block: a type and its 1-based indexes.
type MeterBlock struct {
	Type    byte
	Indexes []int
}

// AppendMeterRequest appends the meter-channel tokens that set the UDP port,
// the report id and the blocks: D3 <port> D4 <id> DC <type idx...>... DE
// (WG-F50). Send them on ChannelMeters and switch back to ChannelData after.
func AppendMeterRequest(dst []byte, port uint16, id uint32, blocks []MeterBlock) ([]byte, error) {
	dst = binary.BigEndian.AppendUint16(append(dst, tokInt16), port)
	dst = binary.BigEndian.AppendUint32(append(dst, tokInt32), id)
	dst = append(dst, tokData)
	for _, b := range blocks {
		if b.Type < MeterChannel || b.Type > 0xAF {
			return dst, ErrMeterRequest
		}
		dst = append(dst, b.Type)
		for _, i := range b.Indexes {
			if i < 1 || i-1 > meterIndexMax {
				return dst, ErrMeterRequest
			}
			dst = append(dst, byte(i-1)) // 0-based on the wire (WG-F50)
		}
	}
	return append(dst, tokEnd), nil
}

// AppendMeterRenew appends the renewal of report id: D4 <id> (WG-F51).
func AppendMeterRenew(dst []byte, id uint32) []byte {
	return binary.BigEndian.AppendUint32(append(dst, tokInt32), id)
}

// MeterPacketID returns the report id of a meter datagram (WG-F52).
func MeterPacketID(pkt []byte) (uint32, bool) {
	if len(pkt) < MeterIDBytes {
		return 0, false
	}
	return binary.BigEndian.Uint32(pkt), true
}

// MeterWord returns word i (0-based, after the report id) of a meter packet.
func MeterWord(pkt []byte, i int) (int16, bool) {
	off := MeterIDBytes + 2*i
	if i < 0 || off+2 > len(pkt) {
		return 0, false
	}
	return int16(binary.BigEndian.Uint16(pkt[off:])), true
}

// MeterDBFS converts a level word to dBFS: word / 256; -32768 (8000) is
// -inf (WG-F52, wing.md section 6).
func MeterDBFS(w int16) float32 {
	if w == math.MinInt16 {
		return float32(math.Inf(-1))
	}
	return float32(w) / meterDBScale
}
