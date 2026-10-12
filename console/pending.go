// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"
	"time"

	"github.com/mixproto/mixcore/model"
)

// A Gain write is in flight from the moment it is accepted until its echo
// arrives or the echo window passes. The state value changes only on the
// echo, so the 6 dB bound and the show-mode raise check must hold against
// both values (review HIGH 1).
const (
	pendingWindow  = 2 * time.Second // the drivers' default echo window (xosc DefaultTiming.EchoWindow)
	pendingMatchDB = 0.25            // half of the 0.5 dB Gain step: the echo of our write
)

type pendingValue struct {
	db float64
	at time.Time
}

// pendingOf returns the value of the last accepted Gain write still in flight.
func (s *safety) pendingOf(p model.ParamRef) (float64, bool) {
	v, ok := s.pending[p]
	if !ok {
		return 0, false
	}
	if time.Since(v.at) > pendingWindow {
		delete(s.pending, p)
		return 0, false
	}
	return v.db, true
}

func (s *safety) notePending(p model.ParamRef, db float64) {
	s.pending[p] = pendingValue{db: db, at: time.Now()}
}

// clearPending forgets the write in flight once its echo arrived.
func (s *safety) clearPending(c Change) {
	v, ok := s.pending[c.Param]
	if ok && math.Abs(dbOf(c.Value)-v.db) <= pendingMatchDB {
		delete(s.pending, c.Param)
	}
}

// gainBounds is the range a Gain write may move to: within 6 dB of the
// console value and of the value in flight.
func gainBounds(cur, pend float64, hasPend bool) (lo, hi float64) {
	lo, hi = cur-gainBoundDB, cur+gainBoundDB
	if hasPend {
		lo, hi = math.Max(lo, pend-gainBoundDB), math.Min(hi, pend+gainBoundDB)
	}
	return lo, hi
}
