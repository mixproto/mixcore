// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package oscsim

import (
	"context"
	"net/netip"
	"slices"
	"time"
)

type meterSub struct {
	addr     netip.AddrPort
	bank     string
	expires  time.Time
	interval time.Duration
	next     time.Time
}

// RegisterMetersLocked starts or renews the stream of bank to from for the
// meter lifetime, one packet per interval (X32-F50, XA-F56). Callers hold the lock.
func (c *Core) RegisterMetersLocked(now time.Time, from netip.AddrPort, bank string, interval time.Duration) {
	sub := meterSub{addr: from, bank: bank, expires: now.Add(c.cfg.MeterTTL), interval: interval, next: now}
	for i := range c.meterSubs {
		if c.meterSubs[i].addr == from && c.meterSubs[i].bank == bank {
			sub.next = c.meterSubs[i].next
			c.meterSubs[i] = sub
			return
		}
	}
	c.meterSubs = append(c.meterSubs, sub)
}

// meterLoop streams every registered bank at its interval until its
// registration expires.
func (c *Core) meterLoop(ctx context.Context) {
	t := time.NewTicker(c.cfg.MeterInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			c.Send(c.dueMeters(now))
		}
	}
}

func (c *Core) dueMeters(now time.Time) []Outgoing {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.meterSubs = slices.DeleteFunc(c.meterSubs, func(m meterSub) bool { return !now.Before(m.expires) })
	if c.OfflineLocked(now) || len(c.meterSubs) == 0 {
		return nil
	}
	// The loop ticks at the meter interval and real timers jitter: a tick a
	// little early must still send, or the stream skips a whole interval and
	// runs at about 12 packets/s instead of 20 (X32-F50, XA-F56).
	slack := c.cfg.MeterInterval / 2
	pkts := map[string][]byte{}
	var out []Outgoing
	for i := range c.meterSubs {
		if c.meterSubs[i].next.Sub(now) > slack {
			continue
		}
		c.meterSubs[i].next = now.Add(c.meterSubs[i].interval)
		bank := c.meterSubs[i].bank
		if _, ok := pkts[bank]; !ok {
			pkts[bank] = c.cfg.Family.MeterPacket(bank)
		}
		if pkts[bank] != nil {
			out = append(out, Outgoing{To: c.meterSubs[i].addr, Pkt: pkts[bank]})
		}
	}
	return out
}
