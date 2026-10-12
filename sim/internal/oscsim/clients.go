// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package oscsim

import (
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// subscriber is one entry of the console's client table (X32-F21, X32-F28,
// XA-F21).
type subscriber struct {
	addr       netip.AddrPort
	since      time.Time
	slotUntil  time.Time // the entry holds a slot until then
	pushUntil  time.Time // pushes go to it until then (its /xremote lifetime)
	noFeedback bool      // registered with "no feedback": never gets its own changes back
}

// ClientSlot is one active entry of the simulated client table.
type ClientSlot struct {
	Addr    netip.AddrPort
	Since   time.Time
	Xremote bool // a live /xremote registration (receives pushes)
}

// RegisterXremoteLocked adds or renews a push client. noFeedback marks a
// client that must not get its own changes back. Callers hold the lock.
func (c *Core) RegisterXremoteLocked(now time.Time, from netip.AddrPort, noFeedback bool) {
	c.touchSlot(now, from, true, noFeedback)
}

// HasPushSlotLocked reports whether from currently receives pushes. Callers hold the lock.
func (c *Core) HasPushSlotLocked(now time.Time, from netip.AddrPort) bool {
	return slices.ContainsFunc(c.subs, func(s subscriber) bool { return s.addr == from && now.Before(s.pushUntil) })
}

// touchSlot adds or renews the client-table entry of from. xremote also
// (re)starts its push lifetime. A newcomer finds no free slot when the table
// is full: it is ignored, or the oldest entry is evicted (option); which one
// the consoles do is unverified (X32-F21, XA-F21).
func (c *Core) touchSlot(now time.Time, from netip.AddrPort, xremote, noFeedback bool) {
	c.subs = slices.DeleteFunc(c.subs, func(sub subscriber) bool { return !now.Before(sub.slotUntil) })
	until := now.Add(c.cfg.SubscriptionTTL)
	i := slices.IndexFunc(c.subs, func(sub subscriber) bool { return sub.addr == from })
	if i < 0 {
		if len(c.subs) >= c.cfg.MaxPushClients {
			if !c.cfg.EvictOldestWhenFull {
				return // default: the newcomer gets no slot and no pushes
			}
			c.subs = c.subs[1:] // oldest registration first
		}
		c.subs = append(c.subs, subscriber{addr: from, since: now})
		i = len(c.subs) - 1
		c.maxSlots = max(c.maxSlots, len(c.subs))
	}
	c.subs[i].slotUntil = until
	if xremote {
		c.subs[i].pushUntil = until
		c.subs[i].noFeedback = noFeedback
	}
}

// PushLocked sends a changed value to every active /xremote client; the
// sender gets it only when toSender is set and it did not ask for no
// feedback. Callers hold the lock.
func (c *Core) PushLocked(now time.Time, addr string, v *Value, sender netip.AddrPort, toSender bool) []Outgoing {
	pkt, err := osc.AppendString(nil, addr, v.Arg())
	if err != nil {
		return nil
	}
	var out []Outgoing
	for _, sub := range c.subs {
		if !now.Before(sub.pushUntil) || (sub.addr == sender && (!toSender || sub.noFeedback)) {
			continue
		}
		out = append(out, Outgoing{To: sub.addr, Pkt: pkt})
	}
	return out
}

// Subscribers returns the addresses of active /xremote clients.
func (c *Core) Subscribers() []netip.AddrPort {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	var out []netip.AddrPort
	for _, sub := range c.subs {
		if now.Before(sub.pushUntil) {
			out = append(out, sub.addr)
		}
	}
	return out
}

// Slots returns the active entries of the client table.
func (c *Core) Slots() []ClientSlot {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	var out []ClientSlot
	for _, sub := range c.subs {
		if now.Before(sub.slotUntil) {
			out = append(out, ClientSlot{Addr: sub.addr, Since: sub.since, Xremote: now.Before(sub.pushUntil)})
		}
	}
	return out
}

// MaxSlotsUsed is the largest number of client-table slots that were in use
// at the same time since New.
func (c *Core) MaxSlotsUsed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSlots
}

// InjectPush sends one arbitrary message to every active /xremote client, as
// a console with unexpected firmware might (a test control: drivers must
// reject what their protocol document does not allow, never guess).
func (c *Core) InjectPush(addr string, args ...osc.Arg) error {
	pkt, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		return err
	}
	c.Do(func(now time.Time) []Outgoing {
		if c.OfflineLocked(now) {
			return nil
		}
		var out []Outgoing
		for _, sub := range c.subs {
			if now.Before(sub.pushUntil) {
				out = append(out, Outgoing{To: sub.addr, Pkt: pkt})
			}
		}
		return out
	})
	return nil
}
