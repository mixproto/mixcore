// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"encoding/binary"
	"net/netip"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// Meter facts (docs/protocols/xair.md section 6).
const (
	// Bank1 is the "ALL CHANNELS" bank (XA-F53).
	Bank1 = "/meters/1"
	// bank1Values: 16 inputs, aux L/R, 4 FX returns L/R, 6 buses, 4 FX
	// sends, main L/R, monitor L/R (XA-F53).
	bank1Values = 40
	// Bank1MainL is the value index of Main L in bank 1; Main R follows (XA-F53).
	Bank1MainL = 36
	// MeterSilence is the int16 value of no signal: -128 dB (XA-F52).
	MeterSilence int16 = -32768
)

// registerMeters starts or renews a stream. Accepted forms: ,s bank and ,si
// bank chnmeterid (XA-F50), and trailing ints as XS10 sends them (XA-F56);
// none of the ints changes the bank 1 stream. Other banks are not modelled.
func (f *family) registerMeters(now time.Time, from netip.AddrPort, m *osc.Message) {
	if bank, ok := m.StringArg(0); ok && bank == Bank1 {
		f.RegisterMetersLocked(now, from, Bank1, f.opts.MeterInterval)
	}
}

// MeterPacket encodes bank 1: an OSC blob holding a little-endian int32
// count, then count little-endian int16 values in 1/256 dB (XA-F51).
func (f *family) MeterPacket(bank string) []byte {
	if bank != Bank1 {
		return nil
	}
	blob := binary.LittleEndian.AppendUint32(nil, bank1Values)
	for _, v := range f.bank1 {
		blob = binary.LittleEndian.AppendUint16(blob, uint16(v))
	}
	pkt, err := osc.AppendString(nil, Bank1, osc.Blob(blob))
	if err != nil {
		return nil
	}
	return pkt
}

// SetMeterValues sets raw bank 1 values from index 0 on, in 1/256 dB
// (0 = 0 dBFS, MeterSilence = no signal; XA-F52). Inputs 1..16 are indexes
// 0..15, Main L/R are Bank1MainL and Bank1MainL+1 (XA-F53).
func (s *Sim) SetMeterValues(values []int16) {
	s.Do(func(time.Time) []oscsim.Outgoing {
		copy(s.bank1[:], values)
		return nil
	})
}

// SetMeterValue sets one raw bank 1 value (ignored outside 0..39).
func (s *Sim) SetMeterValue(index int, value int16) {
	s.Do(func(time.Time) []oscsim.Outgoing {
		if index >= 0 && index < bank1Values {
			s.bank1[index] = value
		}
		return nil
	})
}
