// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"math"
	"slices"

	"github.com/mixproto/mixcore/model"
)

// Settle matching (redesign CC-3, core-api v0.5 rule 4). On a console that
// does not echo Gain, the driver's settle re-read reports the value the
// console holds while a person may still be dragging. Such a report that
// matches one of the drag's own writes (the hold's last write, or any of
// our values in flight, within half a step of the headamp's law) is our
// own write settling: it never ends the hold as an external change. One
// that matches an earlier write than the hold's last is not published at
// all, so the shown value never moves back mid-drag; the next write of the
// drag, or the settle re-read after release, brings the console's value.

// gainSettleLocked classifies a Gain report c against the open holds that
// wrote to its parameter: own is a report of one of our writes, behind one
// of an earlier write than the hold's last. Callers hold mu.
func (s *Supervisor) gainSettleLocked(c Change) (own, behind bool) {
	if c.Param.Field != model.FieldGain {
		return false, false
	}
	latest, held := s.heldLastWriteLocked(c.Param)
	if !held {
		return false, false
	}
	db, tol := dbOf(c.Value), s.halfStepDB(c.Param.Strip.Index)
	same := func(x float64) bool { return math.Abs(x-db) <= tol }
	if same(latest) {
		return true, false
	}
	if slices.ContainsFunc(s.safe.inFlightOf(c.Param), same) {
		return true, true
	}
	return false, false
}

// heldLastWriteLocked returns the last value an open hold on p wrote; ok is
// false when no open hold on p has written.
func (s *Supervisor) heldLastWriteLocked(p model.ParamRef) (last float64, ok bool) {
	for k, w := range s.safe.windows {
		if _, active := s.holds.active[k]; active && k.param == p && w.wrote {
			return w.lastWrite, true
		}
	}
	return 0, false
}
