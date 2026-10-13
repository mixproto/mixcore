// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/model"
)

// In-flight Gain values (review HIGH 1, lead design of review pass 3). Per
// headamp the core keeps every value it sent that no console report has
// matched yet. A non-typed Gain write must be within 6 dB of the console
// value AND of every in-flight value; show mode's floor for "raise" is the
// lowest of them. Gain echo is unverified (X32-F79, XA-F116), so nothing
// expires on a timer:
//   - a report within half a step of an in-flight value removes that one
//     value; any other report only updates the console value;
//   - an authoritative report (the driver's re-read reply of a parameter
//     with unconfirmed writes) clears the set;
//   - stale, adopt, Stop and Pause clear every set; a failed send removes
//     its own value;
//   - a full set (maxInFlight) refuses the next value with ErrGainBound;
//   - a re-send is skipped only when the value is the latest one sent, no
//     report of the parameter arrived since, and it was sent less than
//     the echo window ago (review pass 3 LOW 5, pass 4 MEDIUM 2): a drag held
//     past the cap of a console that does not echo sends the cap once, and
//     a return to an earlier value, or the same value after a report, is a
//     real change and is sent.

const (
	// maxInFlight caps the values in flight per headamp.
	maxInFlight = 8
)

// inFlight is one value sent and not yet matched by a report.
type inFlight struct {
	db     float64
	sentAt time.Time
}

// lastSend is the latest value sent for a parameter with no report since.
type lastSend struct {
	db float64
	at time.Time
}

// inFlightOf returns the values in flight for p (nil: none).
func (s *safety) inFlightOf(p model.ParamRef) []float64 {
	set := s.pending[p]
	if len(set) == 0 {
		return nil
	}
	out := make([]float64, len(set))
	for i, v := range set {
		out[i] = v.db
	}
	return out
}

// noteSent records a value about to be sent. send is false for a value
// sent less than the driver's echo window ago (Capabilities.EchoWindow,
// seats.go echoWindow); ok is false when the set is full and the
// value is not in it.
func (s *safety) noteSent(p model.ParamRef, db float64, now time.Time) (send, ok bool) {
	if l, has := s.latest[p]; has && l.db == db && now.Sub(l.at) < s.echoWindow() {
		return false, true // the same latest value, no report since, within the echo window
	}
	set := s.pending[p]
	if i := slices.IndexFunc(set, func(v inFlight) bool { return v.db == db }); i >= 0 {
		set = slices.Clone(set)
		set[i].sentAt = now
		s.pending[p] = set
	} else {
		if len(set) >= maxInFlight {
			return false, false
		}
		s.pending[p] = append(slices.Clip(set), inFlight{db: db, sentAt: now})
	}
	s.latest[p] = lastSend{db: db, at: now}
	return true, true
}

// forgetSent removes one value whose send failed.
func (s *safety) forgetSent(p model.ParamRef, db float64) {
	if l, has := s.latest[p]; has && l.db == db {
		delete(s.latest, p) // never sent: a retry is a real send
	}
	set := s.pending[p]
	if i := slices.IndexFunc(set, func(v inFlight) bool { return v.db == db }); i >= 0 {
		s.pending[p] = slices.Delete(slices.Clone(set), i, i+1)
	}
	if len(s.pending[p]) == 0 {
		delete(s.pending, p)
	}
}

// noteReport applies a console report of a Gain parameter: an
// authoritative one clears the set; otherwise the in-flight value nearest
// to it within tol (half a step of the law) is removed, if any.
func (s *safety) noteReport(c Change, tol float64) {
	delete(s.latest, c.Param) // after a report the same value is a real change again
	set := s.pending[c.Param]
	if len(set) == 0 {
		return
	}
	if c.Authoritative {
		delete(s.pending, c.Param)
		return
	}
	db, best := dbOf(c.Value), -1
	for i, v := range set {
		if d := math.Abs(v.db - db); d <= tol && (best < 0 || d < math.Abs(set[best].db-db)) {
			best = i
		}
	}
	if best < 0 {
		return
	}
	if rest := slices.Delete(slices.Clone(set), best, best+1); len(rest) > 0 {
		s.pending[c.Param] = rest
	} else {
		delete(s.pending, c.Param)
	}
}

// dropAllPending forgets every value in flight: they belonged to a session
// that is gone (stale, Stop, Pause) or was replaced by a fresh read (adopt).
func (s *safety) dropAllPending() {
	clear(s.pending)
	clear(s.latest)
}

// gainBounds is the range a Gain write may move to: within 6 dB of the
// console value and of every value in flight.
func gainBounds(cur float64, inFlight []float64) (lo, hi float64) {
	lo, hi = cur-gainBoundDB, cur+gainBoundDB
	for _, v := range inFlight {
		lo, hi = math.Max(lo, v-gainBoundDB), math.Min(hi, v+gainBoundDB)
	}
	return lo, hi
}

// raiseFloor is the lowest of the console value and every value in flight:
// above it a write raises Gain (show mode).
func raiseFloor(cur float64, inFlight []float64) float64 {
	floor := cur
	for _, v := range inFlight {
		floor = math.Min(floor, v)
	}
	return floor
}
