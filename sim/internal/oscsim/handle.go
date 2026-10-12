// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package oscsim

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// Class is how the simulator classified a received packet.
type Class uint8

// Packet classes for the write log.
const (
	ClassRead      Class = iota + 1 // parameter read, /xinfo, /info, /status
	ClassSubscribe                  // /xremote, /meters, /renew
	ClassWrite                      // any message with arguments to a parameter or unknown address
	ClassUnknown                    // unknown address without arguments
)

func (c Class) String() string {
	return [...]string{"invalid", "read", "subscribe", "write", "unknown"}[min(int(c), 4)]
}

// Entry is one received packet.
type Entry struct {
	At       time.Time
	From     netip.AddrPort
	Class    Class
	Address  string
	Arg      osc.Arg // first argument (writes); Bytes is a private copy
	Accepted bool    // for writes: the value was applied
}

func (c *Core) handlePacket(from netip.AddrPort, b []byte) {
	var m osc.Message
	if err := osc.Decode(b, &m); err != nil {
		c.cfg.Logger.Debug(c.cfg.Prefix+": malformed packet dropped", "from", from, "err", err)
		return // reaction to garbage is undocumented; the simulator stays silent
	}
	now := time.Now()
	c.mu.Lock()
	if c.OfflineLocked(now) {
		c.mu.Unlock()
		return
	}
	out := c.dispatch(now, from, &m)
	c.mu.Unlock()
	c.Send(out)
}

// dispatch handles one message. Callers hold the lock.
func (c *Core) dispatch(now time.Time, from netip.AddrPort, m *osc.Message) []Outgoing {
	addr := string(m.Address)
	if c.cfg.SlotForAnySender && addr != AddrXremote {
		c.touchSlot(now, from, false, false)
	}
	switch addr {
	case AddrXinfo:
		c.RecordLocked(now, from, ClassRead, addr, osc.Arg{}, false)
		return c.Reply(from, AddrXinfo, osc.String(c.ReportedIPLocked()), osc.String(c.cfg.Name),
			osc.String(c.cfg.Model), osc.String(c.cfg.Firmware))
	case AddrXremote:
		c.RecordLocked(now, from, ClassSubscribe, addr, osc.Arg{}, false)
		c.RegisterXremoteLocked(now, from, false)
		return nil
	}
	if out, handled := c.cfg.Family.Special(now, from, m); handled {
		return out
	}
	v, known := c.cfg.Params[addr]
	switch {
	case known && m.NumArgs() == 0: // read (X32-F04, XA section 2)
		c.RecordLocked(now, from, ClassRead, addr, osc.Arg{}, false)
		return c.Reply(from, addr, v.Arg())
	case m.NumArgs() > 0: // write (X32-F05, XA-F03); unknown addresses count as writes too
		a, _ := m.Arg(0)
		accepted := known && m.NumArgs() == 1 && v.Set(a)
		c.RecordLocked(now, from, ClassWrite, addr, a, accepted)
		if !accepted {
			return nil
		}
		return c.writeEcho(now, addr, v, from)
	default: // unknown address, no reply (X32-F06, XA-F03)
		c.RecordLocked(now, from, ClassUnknown, addr, osc.Arg{}, false)
		return nil
	}
}

// writeEcho is what an applied client write sends: pushes to subscribers and,
// with ReplyToSet, a reply to a sender that got no push.
func (c *Core) writeEcho(now time.Time, addr string, v *Value, from netip.AddrPort) []Outgoing {
	out := c.PushLocked(now, addr, v, from, c.cfg.EchoToSender)
	if !c.cfg.ReplyToSet || slices.ContainsFunc(out, func(o Outgoing) bool { return o.To == from }) {
		return out
	}
	return append(out, c.Reply(from, addr, v.Arg())...)
}

// RecordLocked appends an entry to the write log. Callers hold the lock.
func (c *Core) RecordLocked(now time.Time, from netip.AddrPort, cl Class, addr string, a osc.Arg, accepted bool) {
	a.Bytes = slices.Clone(a.Bytes)
	c.log = append(c.log, Entry{At: now, From: from, Class: cl, Address: addr, Arg: a, Accepted: accepted})
	if cl == ClassWrite {
		c.nWrites++
	}
}

// Log returns a copy of every received packet entry.
func (c *Core) Log() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.log)
}

// Writes returns the received write packets, in arrival order.
func (c *Core) Writes() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Entry
	for _, e := range c.log {
		if e.Class == ClassWrite {
			out = append(out, e)
		}
	}
	return out
}

// WriteCount returns the number of writes received so far (cheap; for polling).
func (c *Core) WriteCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nWrites
}

// ParamLocked returns the stored parameter at addr. Callers hold the lock.
func (c *Core) ParamLocked(addr string) (*Value, bool) {
	v, ok := c.cfg.Params[addr]
	return v, ok
}

// SurfaceSet changes a parameter as the console itself would (an engineer at
// the desk, or another app), pushing it to every /xremote client. While the
// console is offline the change still happens but nobody hears about it.
func (c *Core) SurfaceSet(addr string, a osc.Arg) error {
	var err error
	c.Do(func(now time.Time) []Outgoing {
		v, ok := c.cfg.Params[addr]
		if !ok || !v.Set(a) {
			err = fmt.Errorf("%w or value rejected: %s", ErrUnknownAddress, addr)
			return nil
		}
		if c.OfflineLocked(now) {
			return nil
		}
		return c.PushLocked(now, addr, v, netip.AddrPort{}, true)
	})
	return err
}

// Value returns the stored wire value of addr.
func (c *Core) Value(addr string) (osc.Arg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.cfg.Params[addr]
	if !ok {
		return osc.Arg{}, false
	}
	return v.Arg(), true
}

// Addresses returns every modelled parameter address, sorted (a test oracle
// for tools that harvest the tree from traffic).
func (c *Core) Addresses() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(maps.Keys(c.cfg.Params))
}

// CheckBatchLocked verifies that every address of values is modelled with
// the value's type. Callers hold the lock.
func (c *Core) CheckBatchLocked(values map[string]osc.Arg) error {
	for addr, a := range values {
		if v, ok := c.cfg.Params[addr]; !ok || a.Type != v.typ {
			return fmt.Errorf("%w: %s", ErrUnknownAddress, addr)
		}
	}
	return nil
}

// ApplyBatchLocked applies values in sorted address order and returns the
// pushes of the ones that changed (a recall pushes what changed, X32-F60,
// XA section 7). Callers hold the lock and checked the batch first.
func (c *Core) ApplyBatchLocked(now time.Time, values map[string]osc.Arg) []Outgoing {
	var out []Outgoing
	for _, addr := range slices.Sorted(maps.Keys(values)) {
		v := c.cfg.Params[addr]
		before := v.Arg()
		if !v.Set(values[addr]) || v.Arg().Equal(before) {
			continue
		}
		out = append(out, c.PushLocked(now, addr, v, netip.AddrPort{}, true)...)
	}
	return out
}
