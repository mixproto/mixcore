// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// Addresses with special meaning (docs/protocols/x32.md).
const (
	addrXinfo   = "/xinfo"   // X32-F10
	addrInfo    = "/info"    // X32-F11
	addrXremote = "/xremote" // X32-F20
	addrMeters  = "/meters"  // X32-F50
	bankInputs  = "/meters/1"
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

type outgoing struct {
	to  netip.AddrPort
	pkt []byte
}

func (s *Sim) handlePacket(from netip.AddrPort, b []byte) {
	var m osc.Message
	if err := osc.Decode(b, &m); err != nil {
		s.opts.Logger.Debug("x32 sim: malformed packet dropped", "from", from, "err", err)
		return // reaction to garbage is undocumented; the simulator stays silent
	}
	now := time.Now()
	s.mu.Lock()
	if s.offline(now) {
		s.mu.Unlock()
		return
	}
	out := s.dispatch(now, from, &m)
	s.mu.Unlock()
	for _, o := range out {
		s.send(o.to, o.pkt)
	}
}

// dispatch handles one message. Callers hold s.mu.
func (s *Sim) dispatch(now time.Time, from netip.AddrPort, m *osc.Message) []outgoing {
	addr := string(m.Address)
	if s.opts.SlotForAnySender && addr != addrXremote {
		s.touchSlot(now, from, false)
	}
	switch addr {
	case addrXinfo:
		s.record(now, from, ClassRead, addr, osc.Arg{}, false)
		return s.reply(from, addrXinfo, osc.String(s.reportedIP()), osc.String(s.opts.Name),
			osc.String(s.opts.Model), osc.String(s.opts.Firmware))
	case addrInfo:
		s.record(now, from, ClassRead, addr, osc.Arg{}, false)
		return s.reply(from, addrInfo, osc.String(infoServerVersion), osc.String(infoServerName),
			osc.String(s.opts.Model), osc.String(s.opts.Firmware))
	case addrXremote:
		s.record(now, from, ClassSubscribe, addr, osc.Arg{}, false)
		s.registerXremote(now, from)
		return nil
	case addrMeters:
		s.record(now, from, ClassSubscribe, addr, osc.Arg{}, false)
		s.registerMeters(now, from, m)
		return nil
	}
	v, known := s.params[addr]
	switch {
	case known && m.NumArgs() == 0: // read (X32-F04)
		s.record(now, from, ClassRead, addr, osc.Arg{}, false)
		return s.reply(from, addr, v.arg())
	case m.NumArgs() > 0: // write (X32-F05); unknown addresses count as writes too
		a, _ := m.Arg(0)
		accepted := known && m.NumArgs() == 1 && v.set(a)
		s.record(now, from, ClassWrite, addr, a, accepted)
		if !accepted {
			return nil
		}
		return s.pushLocked(now, addr, v, from, s.opts.EchoToSender)
	default: // unknown address, no reply (X32-F06)
		s.record(now, from, ClassUnknown, addr, osc.Arg{}, false)
		return nil
	}
}

func (s *Sim) record(now time.Time, from netip.AddrPort, c Class, addr string, a osc.Arg, accepted bool) {
	a.Bytes = slices.Clone(a.Bytes)
	s.log = append(s.log, Entry{At: now, From: from, Class: c, Address: addr, Arg: a, Accepted: accepted})
	if c == ClassWrite {
		s.nWrites++
	}
}

func (s *Sim) reportedIP() string {
	if s.opts.IP != "" {
		return s.opts.IP
	}
	if s.pc != nil {
		if ap, err := netip.ParseAddrPort(s.pc.LocalAddr().String()); err == nil {
			return ap.Addr().String()
		}
	}
	return "0.0.0.0"
}

func (s *Sim) reply(to netip.AddrPort, addr string, args ...osc.Arg) []outgoing {
	pkt, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		s.opts.Logger.Error("x32 sim: encode reply", "addr", addr, "err", err)
		return nil
	}
	return []outgoing{{to: to, pkt: pkt}}
}

// registerXremote adds or renews a push client (X32-F20, X32-F21).
func (s *Sim) registerXremote(now time.Time, from netip.AddrPort) { s.touchSlot(now, from, true) }

// touchSlot adds or renews the client-table entry of from (X32-F21, X32-F28).
// xremote also (re)starts its push lifetime. A newcomer finds no free slot when
// the table is full: it is ignored, or the oldest entry is evicted (option).
func (s *Sim) touchSlot(now time.Time, from netip.AddrPort, xremote bool) {
	s.subs = slices.DeleteFunc(s.subs, func(sub subscriber) bool { return !now.Before(sub.slotUntil) })
	until := now.Add(s.opts.SubscriptionTTL)
	i := slices.IndexFunc(s.subs, func(sub subscriber) bool { return sub.addr == from })
	if i < 0 {
		if len(s.subs) >= s.opts.MaxPushClients {
			if !s.opts.EvictOldestWhenFull {
				return // [UNVERIFIED] default: the newcomer gets no slot and no pushes
			}
			s.subs = s.subs[1:] // oldest registration first
		}
		s.subs = append(s.subs, subscriber{addr: from, since: now})
		i = len(s.subs) - 1
		s.maxSlots = max(s.maxSlots, len(s.subs))
	}
	s.subs[i].slotUntil = until
	if xremote {
		s.subs[i].pushUntil = until
	}
}

// registerMeters starts or renews the input meter stream (X32-F50..F52).
func (s *Sim) registerMeters(now time.Time, from netip.AddrPort, m *osc.Message) {
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
	sub := meterSub{addr: from, bank: bank, expires: now.Add(s.opts.MeterTTL), interval: s.opts.MeterInterval * time.Duration(tf), next: now}
	for i := range s.meterSubs {
		if s.meterSubs[i].addr == from && s.meterSubs[i].bank == bank {
			sub.next = s.meterSubs[i].next
			s.meterSubs[i] = sub
			return
		}
	}
	s.meterSubs = append(s.meterSubs, sub)
}

// pushLocked sends a changed value to every active /xremote client except the
// sender (X32-F22, X32-F23). Callers hold s.mu.
func (s *Sim) pushLocked(now time.Time, addr string, v *value, sender netip.AddrPort, toSender bool) []outgoing {
	pkt, err := osc.AppendString(nil, addr, v.arg())
	if err != nil {
		return nil
	}
	var out []outgoing
	for _, sub := range s.subs {
		if !now.Before(sub.pushUntil) || (sub.addr == sender && !toSender) {
			continue
		}
		out = append(out, outgoing{to: sub.addr, pkt: pkt})
	}
	return out
}

// SurfaceSet changes a parameter as an engineer at the desk would, pushing it
// to every /xremote client. While the console is offline the change still
// happens but nobody hears about it.
func (s *Sim) SurfaceSet(addr string, a osc.Arg) error {
	now := time.Now()
	s.mu.Lock()
	v, ok := s.params[addr]
	if !ok || !v.set(a) {
		s.mu.Unlock()
		return fmt.Errorf("%w or value rejected: %s", ErrUnknownAddress, addr)
	}
	var out []outgoing
	if !s.offline(now) {
		out = s.pushLocked(now, addr, v, netip.AddrPort{}, true)
	}
	s.mu.Unlock()
	for _, o := range out {
		s.send(o.to, o.pkt)
	}
	return nil
}

// Value returns the stored wire value of addr.
func (s *Sim) Value(addr string) (osc.Arg, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.params[addr]
	if !ok {
		return osc.Arg{}, false
	}
	return v.arg(), true
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
