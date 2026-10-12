// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"

	"github.com/mixproto/mixcore/model"
)

// A Gain write is in flight from the moment it is sent until the console
// reports that parameter (its echo, a push or a re-read reply). The state
// value changes only on a report, so the 6 dB bound and the show-mode raise
// check hold against both values (review HIGH 1). There is no timer: Gain
// echo is unverified on both families (X32-F79, XA-F116), and a timer let
// an unechoed console move +12 dB in two writes (re-review MEDIUM B). The
// drivers report a re-read reply of a parameter with an unconfirmed write
// even when it equals the last value seen, so a lost write still ends.

// pendingOf returns the value of the last Gain write still in flight.
func (s *safety) pendingOf(p model.ParamRef) (float64, bool) {
	db, ok := s.pending[p]
	return db, ok
}

func (s *safety) notePending(p model.ParamRef, db float64) { s.pending[p] = db }

// forgetPending drops the value in flight when its send failed (only if no
// later write replaced it meanwhile).
func (s *safety) forgetPending(p model.ParamRef, db float64) {
	if v, ok := s.pending[p]; ok && v == db {
		delete(s.pending, p)
	}
}

// dropAllPending forgets every value in flight: they belonged to a session
// that is gone (stale) or was replaced by a fresh read (adopt).
func (s *safety) dropAllPending() { clear(s.pending) }

// clearPending ends the write in flight on any console report of that
// parameter: from then on the reported value is the reference.
func (s *safety) clearPending(c Change) { delete(s.pending, c.Param) }

// gainBounds is the range a Gain write may move to: within 6 dB of the
// console value and of the value in flight.
func gainBounds(cur, pend float64, hasPend bool) (lo, hi float64) {
	lo, hi = cur-gainBoundDB, cur+gainBoundDB
	if hasPend {
		lo, hi = math.Max(lo, pend-gainBoundDB), math.Min(hi, pend+gainBoundDB)
	}
	return lo, hi
}
