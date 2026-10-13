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
// lockout other clients caused, only by its own, and always has a counter
// (security re-check LOW 1): a full table evicts the counter whose last
// failure is oldest. Failures decay after pinFailureDecay without one.
const (
	pinMaxFailures       = 5  // wrong PINs of one client before a lockout
	pinSharedMaxFailures = 10 // wrong PINs of all clients before a shared lockout
	pinLockoutBase       = 30 * time.Second
	pinLockoutMax        = 10 * time.Minute
	maxPINClients        = 64               // clients with a counter; full: only clients with one may try
	pinFailureDecay      = 30 * time.Minute // a client quiet this long starts from zero failures
)

type pinCounter struct {
	failures, inflight, level int
	until                     time.Time // locked out until then
	lastFailure               time.Time // when the last wrong PIN came
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
	c.decay(now)
	return now.Before(c.until) || c.failures+c.inflight >= limit
}

// decay forgets failures, and the growth of the lockout, after
// pinFailureDecay without a wrong PIN and outside a lockout.
func (c *pinCounter) decay(now time.Time) {
	if !c.lastFailure.IsZero() && now.Sub(c.lastFailure) > pinFailureDecay && !now.Before(c.until) {
		c.failures, c.level, c.lastFailure = 0, 0, time.Time{}
	}
}

// settle ends one reserved attempt; a failure may start a lockout.
func (c *pinCounter) settle(failed bool, now time.Time, limit int) {
	c.inflight = max(c.inflight-1, 0)
	if !failed {
		return
	}
	c.lastFailure = now
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

// counter returns client's counter. A full table makes room by evicting
// the counter whose last failure is oldest (none in flight); the owner
// always gets one. nil: full and nothing to evict.
func (g *pinGuard) counter(client string, owner bool) *pinCounter {
	if g.clients == nil {
		g.clients = map[string]*pinCounter{}
	}
	if c, ok := g.clients[client]; ok {
		return c
	}
	if len(g.clients) >= maxPINClients && !g.evictOne() && !owner {
		return nil
	}
	c := &pinCounter{}
	g.clients[client] = c
	return c
}

// evictOne drops the counter whose last failure is oldest and that has no
// attempt in flight; false when there is none.
func (g *pinGuard) evictOne() bool {
	victim := ""
	for id, c := range g.clients {
		if c.inflight == 0 && (victim == "" || c.lastFailure.Before(g.clients[victim].lastFailure)) {
			victim = id
		}
	}
	if victim == "" {
		return false
	}
	delete(g.clients, victim)
	return true
}

// pinReservation is one attempt reserved before the hash.
type pinReservation struct {
	record *pinRecord
	client *pinCounter
	shared bool // the attempt also counts on the shared counter (security re-check LOW 2)
}

// reservePINLocked checks that origin may try a PIN now and reserves the
// attempt.
func (s *Supervisor) reservePINLocked(origin driver.Origin) (pinReservation, error) {
	if err := s.checkConsoleRoleLocked(origin); err != nil {
		return pinReservation{}, err
	}
	if s.lock.record == nil {
		return pinReservation{}, fmt.Errorf("%w: show mode is not locked", driver.ErrUnsupported)
	}
	now, g := time.Now(), &s.lock.guard
	owner := s.safe.role(origin.ClientID).Kind == RoleOwner
	c := g.counter(origin.ClientID, owner)
	if g.hashing || c == nil || c.blocked(now, pinMaxFailures) || (!owner && g.shared.blocked(now, pinSharedMaxFailures)) {
		return pinReservation{}, fmt.Errorf("%w: no PIN attempt now (busy or locked out)", ErrRole)
	}
	g.hashing = true
	c.inflight++
	if !owner { // the owner's attempts never count on the shared counter
		g.shared.inflight++
	}
	return pinReservation{record: s.lock.record, client: c, shared: !owner}, nil
}

// settlePINLocked ends a reserved attempt: the hash is done. The shared
// count is settled by what the reservation took, whatever the role is now.
func (s *Supervisor) settlePINLocked(r pinReservation, failed bool) {
	now, g := time.Now(), &s.lock.guard
	g.hashing = false
	r.client.settle(failed, now, pinMaxFailures)
	if r.shared {
		g.shared.settle(failed, now, pinSharedMaxFailures)
	}
}
