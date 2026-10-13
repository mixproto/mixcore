// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"fmt"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Online PIN guessing (review security M1, go S5, sec L3; lead decisions).
// An attempt is reserved under the supervisor lock before the PIN is hashed
// (failures + attempts in flight at the limit are refused), and only one
// hash runs at a time, so parallel guesses cannot pass the limit. Each
// client has its own counter; all clients share a higher one. A full
// counter locks its clients out for pinLockoutBase, doubling with each
// lockout up to pinLockoutMax. The owner is never held by the shared
// lockout other clients caused, only by its own.
const (
	pinMaxFailures       = 5  // wrong PINs of one client before a lockout
	pinSharedMaxFailures = 10 // wrong PINs of all clients before a shared lockout
	pinLockoutBase       = 30 * time.Second
	pinLockoutMax        = 10 * time.Minute
	maxPINClients        = 64 // clients with a counter; full: only clients with one may try
)

type pinCounter struct {
	failures, inflight, level int
	until                     time.Time // locked out until then
}

// pinLockout is the lockout after the level-th full counter: 30 s, 60 s,
// 120 s, ... at most pinLockoutMax.
func pinLockout(level int) time.Duration {
	if level < 1 {
		level = 1
	}
	if level > 10 {
		return pinLockoutMax
	}
	return min(pinLockoutBase<<(level-1), pinLockoutMax)
}

// blocked reports a counter that may not try now.
func (c *pinCounter) blocked(now time.Time, limit int) bool {
	return now.Before(c.until) || c.failures+c.inflight >= limit
}

// settle ends one reserved attempt; a failure may start a lockout.
func (c *pinCounter) settle(failed bool, now time.Time, limit int) {
	c.inflight = max(c.inflight-1, 0)
	if !failed {
		return
	}
	if c.failures++; c.failures >= limit {
		c.level++
		c.failures, c.until = 0, now.Add(pinLockout(c.level))
	}
}

// pinGuard is the guessing state of one PIN lock. Guarded by Supervisor.mu.
type pinGuard struct {
	clients map[string]*pinCounter
	shared  pinCounter
	hashing bool // one hash at a time
}

// counter returns client's counter, or nil when the table is full.
func (g *pinGuard) counter(client string, now time.Time) *pinCounter {
	if g.clients == nil {
		g.clients = map[string]*pinCounter{}
	}
	if c, ok := g.clients[client]; ok {
		return c
	}
	if len(g.clients) >= maxPINClients {
		for id, c := range g.clients { // forget clients with nothing pending
			if c.failures == 0 && c.inflight == 0 && !now.Before(c.until) {
				delete(g.clients, id)
			}
		}
		if len(g.clients) >= maxPINClients {
			return nil
		}
	}
	c := &pinCounter{}
	g.clients[client] = c
	return c
}

// reservePINLocked checks that origin may try a PIN now and reserves the
// attempt; it returns the record to check and the client's counter.
func (s *Supervisor) reservePINLocked(origin driver.Origin) (*pinRecord, *pinCounter, error) {
	if err := s.checkConsoleRoleLocked(origin); err != nil {
		return nil, nil, err
	}
	if s.lock.record == nil {
		return nil, nil, fmt.Errorf("%w: show mode is not locked", driver.ErrUnsupported)
	}
	now, g := time.Now(), &s.lock.guard
	c := g.counter(origin.ClientID, now)
	owner := s.safe.role(origin.ClientID).Kind == RoleOwner
	if g.hashing || c == nil || c.blocked(now, pinMaxFailures) || (!owner && g.shared.blocked(now, pinSharedMaxFailures)) {
		return nil, nil, fmt.Errorf("%w: no PIN attempt now (busy or locked out)", ErrRole)
	}
	g.hashing = true
	c.inflight++
	if !owner {
		g.shared.inflight++
	}
	return s.lock.record, c, nil
}

// settlePINLocked ends a reserved attempt: the hash is done.
func (s *Supervisor) settlePINLocked(origin driver.Origin, c *pinCounter, failed bool) {
	now, g := time.Now(), &s.lock.guard
	g.hashing = false
	c.settle(failed, now, pinMaxFailures)
	if s.safe.role(origin.ClientID).Kind != RoleOwner { // the owner's failures never lock the others out
		g.shared.settle(failed, now, pinSharedMaxFailures)
	}
}
