// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"encoding/binary"
	"math"
	"net/netip"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// Addresses with special meaning (docs/protocols/x32.md).
const (
	addrInfo   = "/info"   // X32-F11
	addrMeters = "/meters" // X32-F50
	bankInputs = "/meters/1"
	// bankInputsAlt is the unslashed spelling printed in the command table (X32-F52).
	bankInputsAlt     = "meters/1"
	bankBuses         = "/meters/2"
	meterBank1Values  = 96 // X32-F51: 32 inputs + 32 gate GR + 32 dynamics GR
	meterBank2Values  = 49 // X32-F55: 16 buses, 6 matrices, main L, R, M/C, 24 GR
	bank2MainL        = 22 // X32-F55
	infoServerVersion = "V2.05"
	infoServerName    = "osc-server"
	minTimeFactor     = 1
	maxTimeFactor     = 99
)

// family implements oscsim.Family for the X32. Its methods run with the core lock held.
type family Sim

// Special handles /info and /meters (X32-F11, X32-F50).
func (f *family) Special(now time.Time, from netip.AddrPort, m *osc.Message) ([]oscsim.Outgoing, bool) {
	addr := string(m.Address)
	switch addr {
	case addrInfo:
		f.RecordLocked(now, from, oscsim.ClassRead, addr, osc.Arg{}, false)
		return f.Reply(from, addrInfo, osc.String(infoServerVersion), osc.String(infoServerName),
			osc.String(f.opts.Model), osc.String(f.opts.Firmware)), true
	case addrMeters:
		f.RecordLocked(now, from, oscsim.ClassSubscribe, addr, osc.Arg{}, false)
		f.registerMeters(now, from, m)
		return nil, true
	}
	return nil, false
}

// registerMeters starts or renews a meter stream (X32-F50..F52, X32-F55).
func (f *family) registerMeters(now time.Time, from netip.AddrPort, m *osc.Message) {
	bank, ok := m.StringArg(0)
	switch {
	case !ok:
		return
	case bank == bankInputsAlt:
		bank = bankInputs
	case bank != bankInputs && bank != bankBuses:
		return // other banks are not modelled
	}
	tf := int32(minTimeFactor)
	if v, ok := m.IntArg(1); ok && v >= minTimeFactor && v <= maxTimeFactor {
		tf = v
	}
	f.RegisterMetersLocked(now, from, bank, f.opts.MeterInterval*time.Duration(tf))
}

// MeterPacket encodes one bank (X32-F51, X32-F53, X32-F55).
func (f *family) MeterPacket(bank string) []byte {
	var blob []byte
	if bank == bankBuses {
		var levels [bank2MainL + 2]float32
		levels[bank2MainL], levels[bank2MainL+1] = f.mainLR[0], f.mainLR[1]
		blob = appendMeterBlob(nil, meterBank2Values, levels[:])
	} else {
		blob = appendMeterBlob(nil, meterBank1Values, f.levels[:])
	}
	pkt, err := osc.AppendString(nil, bank, osc.Blob(blob))
	if err != nil {
		return nil
	}
	return pkt
}

// appendMeterBlob encodes a bank as described in X32-F53: little-endian
// count, then count little-endian float32 values; values beyond len(levels)
// are 0.
func appendMeterBlob(dst []byte, count int, levels []float32) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(count))
	for i := range count {
		var v float32
		if i < len(levels) {
			v = levels[i]
		}
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(v))
	}
	return dst
}
