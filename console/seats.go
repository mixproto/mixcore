// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// The monitor ceiling's reference is the engineer's latest value (SR-28).
// The console cannot say who changed a value (open question 46), so every
// console report is judged by the lead's rule (security re-checks of
// 4fc64de and ad7c30c), built so that any error leans to the safe side, a
// lower ceiling. "The window" is the driver's echo window
// (Capabilities.EchoWindow): a write older than it can no longer come
// back as its echo.
//
//   - Lower: a report below the reference is always credited, whoever
//     sent it: the reference only follows it down.
//   - Raise: a report above the reference is credited only if it matches
//     (within half a grid step) none of the values monitor seats sent for
//     that parameter, and is not our own write read back. Those values are
//     kept per parameter, at most maxSeatValues, the oldest dropped first;
//     a drop is an overflow, and while it lasts no raise is credited.
//   - A credit forgets the seat values older than the window (the
//     console's value is now the engineer's; younger ones could still come
//     back as late echoes, so they stay).
//   - An overflow ends with a credited cut or the full read of a connect,
//     reconnect or resume, but only once the newest dropped value is older
//     than the window (a dropped value could echo until then).
//   - A role change or revoke does not drop a seat's values younger than
//     the window (the echo of its last raise must never be credited); they
//     stop counting once older, and its older values go at once.
//
// Raise caution: an engineer raise at the desk to exactly a value a seat
// sent recently (within the window of the last credit, or since it) is not
// credited: the seats keep the lower ceiling until the engineer moves the
// value again or the next full read. Rare, and in the safe direction.

const (
	// maxSeatValues bounds the seat values kept per parameter.
	maxSeatValues = 64
	// lateEchoWindowDefault is the window when the driver does not report
	// its echo window (Capabilities.EchoWindow 0).
	lateEchoWindowDefault = 2 * time.Second
)

// sameLevel reports whether two levels of p are one console value (half
// a step of p's grid apart at most); see Supervisor.sameLevelLocked.
type sameLevel func(p model.ParamRef, a, b float64) bool

type seatValue struct {
	client  string
	db      float64
	at      time.Time
	revoked bool // its seat lost the role: it counts only within the window
}

// seatLog is the seat values of one parameter. Guarded by Supervisor.mu.
type seatLog struct {
	values    []seatValue // oldest first
	overflow  bool        // a value was dropped: no raise is credited
	droppedAt time.Time   // when the newest value was dropped
	ending    bool        // a cut or full read asked to end the overflow
}

// echoWindow is the late-echo window of the current session.
func (s *safety) echoWindow() time.Duration {
	return cmp.Or(s.window, lateEchoWindowDefault)
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
		lg.overflow, lg.droppedAt, lg.ending = true, now, false
	}
}

// credit judges one console report v of p (see the rule above) and applies it.
func (s *safety) credit(p model.ParamRef, v float64, ownWrite bool, same sameLevel, now time.Time) {
	win := s.echoWindow()
	lg := s.seats[p]
	if lg != nil {
		lg.settle(now, win)
	}
	ref, has := s.engineer[p]
	if !has {
		ref = math.Inf(-1)
	}
	lower := has && (v < ref || same(p, v, ref))
	if !lower && (ownWrite || (lg != nil && (lg.overflow || lg.matches(p, v, same)))) {
		return // a raise that may be a seat's: never credited
	}
	s.engineer[p] = v
	if lg != nil {
		lg.values = slices.DeleteFunc(lg.values, func(sv seatValue) bool { return now.Sub(sv.at) > win })
		lg.endOverflow(now, win)
	}
}

// settle drops revoked values older than the window and ends an overflow
// asked to end once its newest drop is older than the window.
func (lg *seatLog) settle(now time.Time, win time.Duration) {
	lg.values = slices.DeleteFunc(lg.values, func(sv seatValue) bool { return sv.revoked && now.Sub(sv.at) > win })
	if lg.ending && now.Sub(lg.droppedAt) > win {
		lg.overflow, lg.ending = false, false
	}
}

// endOverflow ends an overflow (a cut or a full read), at once only if no
// dropped value can still echo; otherwise once the window has passed.
func (lg *seatLog) endOverflow(now time.Time, win time.Duration) {
	if !lg.overflow {
		return
	}
	lg.ending = true
	lg.settle(now, win)
}

func (lg *seatLog) matches(p model.ParamRef, v float64, same sameLevel) bool {
	return slices.ContainsFunc(lg.values, func(sv seatValue) bool { return same(p, sv.db, v) })
}

// forgetSeat is a role change or revoke of client: its values older than
// the window go now, the younger ones count until they are older.
func (s *safety) forgetSeat(client string, now time.Time) {
	win := s.echoWindow()
	for _, lg := range s.seats {
		lg.values = slices.DeleteFunc(lg.values, func(sv seatValue) bool { return sv.client == client && now.Sub(sv.at) > win })
		for i := range lg.values {
			if lg.values[i].client == client {
				lg.values[i].revoked = true
			}
		}
	}
}
