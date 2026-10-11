// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

// errBadMeters marks a meter packet that does not match X32-F53.
var errBadMeters = errors.New("x32: malformed meter packet")

const (
	meterCountSize = 4 // little-endian int32 count (X32-F53)
	meterValueSize = 4 // little-endian float32 per value (X32-F53)
)

// meterPrefix is the padded address of a bank 1 packet; it lets the reader
// recognise meter packets before decoding them.
var meterPrefix = []byte(meterBank1 + "\x00\x00\x00")

// meterSink decodes bank 1 packets in the subscription reader goroutine and
// publishes them. It owns its frame and message; nothing here allocates.
type meterSink struct {
	slot    *meters.Slot
	enabled atomic.Bool
	frame   meters.Frame
	msg     osc.Message
}

func newMeterSink(slot *meters.Slot) *meterSink { return &meterSink{slot: slot} }

// handle reports whether pkt was a meter packet (consumed, even if dropped).
func (m *meterSink) handle(pkt []byte, at time.Time) bool {
	if !bytes.HasPrefix(pkt, meterPrefix) {
		return false
	}
	if m.slot == nil || !m.enabled.Load() {
		return true // meters keep arriving for up to 10 s after we stop renewing (X32-F50)
	}
	if err := osc.Decode(pkt, &m.msg); err != nil {
		return true
	}
	blob, ok := m.msg.BlobArg(0)
	if !ok || decodeMeters1(blob, &m.frame) != nil {
		return true
	}
	m.frame.Seq++
	m.frame.At = at
	m.slot.Publish(&m.frame)
	return true
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
