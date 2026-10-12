// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"net/netip"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// Addresses with special meaning (docs/protocols/xair.md section 2).
const (
	addrInfo       = "/info"       // XA-F10
	addrStatus     = "/status"     // XA-F16
	addrXremoteNFB = "/xremotenfb" // XA-F23: no feedback to the initiator
	addrMeters     = "/meters"     // XA-F50
	// infoServerVersion is the first /info field of the XS4 example (XA-F10).
	infoServerVersion = "V0.04"
	statusActive      = "active"  // XA-F16
	statusStandby     = "standby" // XA-F16
)

// family implements oscsim.Family for X Air. Its methods run with the core lock held.
type family Sim

// Special handles /info, /status, /xremotenfb, /meters and the snapshot
// actions; everything else is a plain parameter.
func (f *family) Special(now time.Time, from netip.AddrPort, m *osc.Message) ([]oscsim.Outgoing, bool) {
	addr := string(m.Address)
	switch addr {
	case addrInfo: // XA-F10: server version, device name (not "osc-server"), model, firmware
		f.RecordLocked(now, from, oscsim.ClassRead, addr, osc.Arg{}, false)
		return f.Reply(from, addrInfo, osc.String(infoServerVersion), osc.String(f.opts.Name),
			osc.String(f.opts.Model), osc.String(f.opts.Firmware)), true
	case addrStatus:
		f.RecordLocked(now, from, oscsim.ClassRead, addr, osc.Arg{}, false)
		return f.Reply(from, addrStatus, osc.String(f.status(now, from)), osc.String(f.ReportedIPLocked()),
			osc.String(f.opts.Name)), true
	case addrXremoteNFB:
		f.RecordLocked(now, from, oscsim.ClassSubscribe, addr, osc.Arg{}, false)
		f.RegisterXremoteLocked(now, from, true)
		return nil, true
	case addrMeters:
		f.RecordLocked(now, from, oscsim.ClassSubscribe, addr, osc.Arg{}, false)
		f.registerMeters(now, from, m)
		return nil, true
	case addrSnapIdx:
		if m.NumArgs() == 0 {
			return nil, false // a plain read
		}
		a, _ := m.Arg(0)
		f.RecordLocked(now, from, oscsim.ClassWrite, addr, a, false) // read-only (XA-F60)
		return nil, true
	case addrSnapLoad, addrSnapSave, addrSnapDel:
		return f.snapAction(now, from, addr, m), true
	}
	return nil, false
}

// status is "active" unless the option makes an unserved port "standby" (XA-F16).
func (f *family) status(now time.Time, from netip.AddrPort) string {
	if f.opts.StandbyWhenNotServed && !f.HasPushSlotLocked(now, from) {
		return statusStandby
	}
	return statusActive
}
