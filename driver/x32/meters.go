// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sync"
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

// Meter frame layout (contracts/core-api/meter-frame.md): inputs 1..32, then
// Main L and Main R.
const (
	mainLLevel  = inputChannels     // level index of Main L; Main R follows
	meterLevels = inputChannels + 2 // levels in a frame
	bank2MainL  = 22                // X32-F55: value index of Main L in bank 2; Main R follows
)

// Padded addresses of bank 1 and bank 2 packets; they let the reader
// recognise meter packets before decoding them.
var (
	meterPrefix1 = []byte(meterBank1 + "\x00\x00\x00")
	meterPrefix2 = []byte(meterBank2 + "\x00\x00\x00")
)

// meterSink decodes meter packets in the subscription reader goroutine and
// publishes them. It owns its frame and message; nothing here allocates.
type meterSink struct {
	slot *meters.Slot
	// mu orders publishing with switching off: once switchOn(false) returns,
	// no frame is published, so a Slot.Reset after it stays empty.
	mu      sync.Mutex
	enabled atomic.Bool
	reset   atomic.Bool // set when meters are switched on: forget old levels first
	frame   meters.Frame
	msg     osc.Message
}

func newMeterSink(slot *meters.Slot) *meterSink { return &meterSink{slot: slot} }

// switchOn enables publishing; levels of a previous session are dropped.
func (m *meterSink) switchOn(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if on {
		m.reset.Store(true)
	}
	m.enabled.Store(on)
}

// handle reports whether pkt was a meter packet (consumed, even if dropped).
func (m *meterSink) handle(pkt []byte, at time.Time) bool {
	bank1 := bytes.HasPrefix(pkt, meterPrefix1)
	if !bank1 && !bytes.HasPrefix(pkt, meterPrefix2) {
		return false
	}
	if m.slot == nil || !m.enabled.Load() {
		return true // meters keep arriving for up to 10 s after we stop renewing (X32-F50)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled.Load() {
		return true // switched off meanwhile
	}
	if m.reset.CompareAndSwap(true, false) {
		m.silence()
	}
	if osc.Decode(pkt, &m.msg) != nil {
		return true
	}
	blob, ok := m.msg.BlobArg(0)
	if !ok || m.decode(bank1, blob) != nil {
		return true
	}
	m.frame.Count = meterLevels
	m.frame.Seq++
	m.frame.At = at
	m.slot.Publish(&m.frame)
	return true
}

func (m *meterSink) silence() {
	for i := range meterLevels {
		m.frame.Levels[i] = float32(math.Inf(-1))
	}
}

// decode updates the inputs (bank 1) or Main L/R (bank 2) of the frame.
func (m *meterSink) decode(bank1 bool, blob []byte) error {
	if !bank1 {
		return decodeMainLR(blob, &m.frame)
	}
	var in meters.Frame // stack copy keeps Main L/R of m.frame; no allocation
	if err := decodeMeters1(blob, &in); err != nil {
		return err
	}
	copy(m.frame.Levels[:in.Count], in.Levels[:in.Count])
	for i := in.Count; i < inputChannels; i++ {
		m.frame.Levels[i] = float32(math.Inf(-1))
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
