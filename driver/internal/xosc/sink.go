// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
)

// MeterCodec is the family's meter format. Implementations are stateless
// apart from the frame they are given, and allocate nothing.
type MeterCodec interface {
	// Bank returns the index of the meter bank pkt belongs to, judged by its
	// padded OSC address prefix, or ok=false for any other packet.
	Bank(pkt []byte) (bank int, ok bool)
	// Decode updates fr.Levels from the blob of one bank packet, converted to
	// dBFS (invariant 7). Levels it does not own stay as they are.
	Decode(bank int, blob []byte, fr *meters.Frame) error
	// Levels is the frame's level count (contracts/core-api/meter-frame.md).
	Levels() int
	// Requests are the /meters argument lists that start every stream, sent
	// on every renewal (X32-F50, XA-F57).
	Requests() [][]osc.Arg
}

// MeterSink decodes meter packets in the subscription reader goroutine and
// publishes them. It owns its frame and message; nothing here allocates.
type MeterSink struct {
	slot  *meters.Slot
	codec MeterCodec
	// mu orders publishing with switching off: once SwitchOn(false) returns,
	// no frame is published, so a Slot.Reset after it stays empty.
	mu      sync.Mutex
	enabled atomic.Bool
	reset   atomic.Bool // set when meters are switched on: forget old levels first
	// absentFrom..absentTo are levels of input strips the console did not
	// answer (XA-F72): published as -inf whatever the bank carries.
	absentFrom, absentTo atomic.Int32
	frame                meters.Frame
	msg                  osc.Message
}

// NewMeterSink returns a sink that publishes to slot (nil: meters dropped).
func NewMeterSink(codec MeterCodec, slot *meters.Slot) *MeterSink {
	return &MeterSink{slot: slot, codec: codec}
}

// SwitchOn enables publishing; levels of a previous session are dropped.
func (m *MeterSink) SwitchOn(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if on {
		m.reset.Store(true)
	}
	m.enabled.Store(on)
}

// Handle reports whether pkt was a meter packet (consumed, even if dropped).
func (m *MeterSink) Handle(pkt []byte, at time.Time) bool {
	bank, ok := m.codec.Bank(pkt)
	if !ok {
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
	if !ok || m.codec.Decode(bank, blob, &m.frame) != nil {
		return true
	}
	for i := m.absentFrom.Load(); i < m.absentTo.Load(); i++ {
		m.frame.Levels[i] = float32(math.Inf(-1))
	}
	m.frame.Count = m.codec.Levels()
	m.frame.Seq++
	m.frame.At = at
	m.slot.Publish(&m.frame)
	return true
}

// SetAbsent makes levels from..to-1 read -inf (input strips the console did
// not answer); from >= to clears it. Indexes are clamped to the frame.
func (m *MeterSink) SetAbsent(from, to int) {
	from, to = max(from, 0), min(to, m.codec.Levels())
	if from >= to {
		from, to = 0, 0
	}
	m.absentFrom.Store(int32(from))
	m.absentTo.Store(int32(to))
}

func (m *MeterSink) silence() {
	for i := range m.codec.Levels() {
		m.frame.Levels[i] = float32(math.Inf(-1))
	}
}
