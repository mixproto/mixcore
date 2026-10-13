// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// The monitor ceiling's reference is the engineer's latest value (SR-28).
// The console cannot say who changed a value (open question 46), so every
// console report is judged by the lead's rule (security re-check of
// 4fc64de), built so that any error leans to the safe side, a lower
// ceiling:
//
//   - Lower: a report below the reference is always credited, whoever
//     sent it: the reference only follows it down.
//   - Raise: a report above the reference is credited only if it matches
//     (within half a grid step) none of the values monitor seats sent for
//     that parameter. Those values are kept per parameter, at most
//     maxSeatValues, the oldest dropped first; a drop is an overflow, and
//     while it lasts no raise is credited.
//   - A credit forgets the seat values older than the echo window
//     (lateEchoWindow): the console's value is now the engineer's, and a
//     write older than that cannot come back as a late echo; younger ones
//     still could (re-check B after a cut), so they stay. An overflow ends
//     with a credited cut, or with the full read of the next connect,
//     reconnect or resume (no hold can be open then).
//   - A role change or revoke forgets that seat's values.
//
// Raise caution: an engineer raise at the desk to exactly a value a seat
// sent recently (within lateEchoWindow of the last credit, or since it) is
// not credited: the seats keep the lower ceiling until the engineer moves
// the value again or the next full read. Rare, and in the safe direction.

const (
	// maxSeatValues bounds the seat values kept per parameter.
	maxSeatValues = 64
	// lateEchoWindow is how long a seat's write can still come back as an
	// echo (the drivers' echo window, xosc DefaultTiming.EchoWindow).
	lateEchoWindow = resendEvery
)

// sameLevel reports whether two levels of p are one console value (half
// a step of p's grid apart at most); see Supervisor.sameLevelLocked.
type sameLevel func(p model.ParamRef, a, b float64) bool

type seatValue struct {
	client string
	db     float64
	at     time.Time
}

// seatLog is the seat values of one parameter. Guarded by Supervisor.mu.
type seatLog struct {
	values   []seatValue // oldest first
	overflow bool        // a value was dropped: no raise is credited
}

// noteSeatSent remembers a monitor client's write that left for the console.
func (s *safety) noteSeatSent(o driver.Origin, p model.ParamRef, db float64, same sameLevel, now time.Time) {
	if !isCeilingField(p.Field) || !s.isMonitorOrigin(o) {
		return
	}
	lg := s.seats[p]
	if lg == nil {
		lg = &seatLog{}
		s.seats[p] = lg
	}
	lg.values = slices.DeleteFunc(lg.values, func(v seatValue) bool { return v.client == o.ClientID && same(p, v.db, db) })
	lg.values = append(lg.values, seatValue{client: o.ClientID, db: db, at: now})
	if len(lg.values) > maxSeatValues {
		lg.values = slices.Delete(lg.values, 0, len(lg.values)-maxSeatValues)
		lg.overflow = true
	}
}

// credit judges one console report v of p (see the rule above) and applies it.
func (s *safety) credit(p model.ParamRef, v float64, ownWrite bool, same sameLevel, now time.Time) {
	ref, has := s.engineer[p]
	if !has {
		ref = math.Inf(-1)
	}
	lower := has && (v < ref || same(p, v, ref))
	lg := s.seats[p]
	if !lower && (ownWrite || (lg != nil && (lg.overflow || lg.matches(p, v, same)))) {
		return // a raise that may be a seat's: never credited
	}
	s.engineer[p] = v
	if lg != nil {
		lg.values = slices.DeleteFunc(lg.values, func(sv seatValue) bool { return now.Sub(sv.at) > lateEchoWindow })
		lg.overflow = false
	}
}

func (lg *seatLog) matches(p model.ParamRef, v float64, same sameLevel) bool {
	return slices.ContainsFunc(lg.values, func(sv seatValue) bool { return same(p, sv.db, v) })
}

// forgetSeat drops every value a client sent (a role change or revoke).
func (s *safety) forgetSeat(client string) {
	for _, lg := range s.seats {
		lg.values = slices.DeleteFunc(lg.values, func(sv seatValue) bool { return sv.client == client })
	}
}
