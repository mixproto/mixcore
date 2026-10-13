// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"
	"slices"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Settle matching (redesign CC-3, core-api v0.5 rule 4, review blockers 1
// and 2). On a console that does not echo Gain, the driver's settle re-read
// reports the value the console holds while a person may still be dragging.
// Only two kinds of report can be the drag's own write: the holding
// client's own echo (its origin), and an authoritative read-back reply
// (driver.ParamChanged.Authoritative). Such a report that matches the
// hold's last write or one of our values in flight (within half a step of
// the headamp's law) never ends the hold. A holder echo of an earlier write
// is not published (the shown value never moves back mid-drag); an
// authoritative reply always is: it is the console's value. Everything else
// (the desk, a scene recall, another client's echo) is an external change,
// even when its value matches one of ours.

// gainSettleLocked classifies a Gain report c against the open hold that
// wrote to its parameter: own is a report of one of our writes, behind a
// holder echo of an earlier write than the hold's last. Callers hold mu.
func (s *Supervisor) gainSettleLocked(c Change) (own, behind bool) {
	if c.Param.Field != model.FieldGain {
		return false, false
	}
	holder, latest, held := s.heldLastWriteLocked(c.Param)
	if !held || (!c.Authoritative && !isOwnEcho(c.Origin, holder)) {
		return false, false
	}
	db, tol := dbOf(c.Value), s.halfStepDB(c.Param.Strip.Index)
	same := func(x float64) bool { return math.Abs(x-db) <= tol }
	switch {
	case same(latest):
		return true, false
	case slices.ContainsFunc(s.safe.inFlightOf(c.Param), same):
		return true, !c.Authoritative
	}
	return false, false
}

// heldLastWriteLocked returns the client holding p and the last value its
// hold wrote (before the first write: its start, the newest value in
// flight); ok is false when no hold on p is open.
func (s *Supervisor) heldLastWriteLocked(p model.ParamRef) (client string, last float64, ok bool) {
	for k, w := range s.safe.windows {
		if _, active := s.holds.active[k]; active && k.param == p {
			return k.client, w.lastWrite, true
		}
	}
	return "", 0, false
}

// endOtherGainHoldsLocked ends every other client's hold on headamp Gain p
// when origin grabs it (review blocker 1, SR-15): two fingers on one
// preamp from two devices would bound each against the other's writes.
func (s *Supervisor) endOtherGainHoldsLocked(p model.ParamRef, origin driver.Origin) {
	if !isHeadampGain(p) {
		return
	}
	cur, _ := s.state.Value(p)
	s.holds.external(Change{Param: p, Value: cur, Origin: origin})
	s.closeEndedWindowsLocked()
}
