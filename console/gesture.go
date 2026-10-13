// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Apply sends one user write to the console with the safety rules of a
// plain write (Write without flags). See Write.
func (s *Supervisor) Apply(cmd driver.Command) error {
	_, err := s.Write(Write{Command: cmd})
	return err
}

// Write sends one user write to the console. It never queues: while the
// state is not live it returns driver.ErrStale (rule 3). A write for a
// parameter whose gesture was ended returns ErrGestureEnded until a new
// hold-begin. Inside a hold the value is a candidate the gesture window
// clamps (CR-22); the monitor ceiling, show mode (CR-24), the Gain bound
// (CR-23) and the big-typed-raise rule (CR-26) apply to every write. The
// Result says what was applied; a write that changes nothing is not sent.
func (s *Supervisor) Write(w Write) (Result, error) {
	if !w.Origin.MayWrite() {
		return Result{}, driver.ErrNotUserOrigin // rule 2
	}
	if (w.Typed || w.Confirmed) && w.Origin.Kind != driver.OriginGesture {
		return Result{}, ErrTypedOrigin
	}
	res, err := s.checkAndApply(&w)
	if errors.Is(err, driver.ErrClosed) {
		return Result{}, driver.ErrStale // the session died between the check and the call
	}
	return res, err
}

// checkAndApply checks a write and hands it to the session under one hold
// of mu (released even if a rule panics), so no role change, hold end or
// stale can slip between the check and the send (review Sec L4).
// Session.Apply never blocks and never calls back into the supervisor.
func (s *Supervisor) checkAndApply(w *Write) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(w, true)
}

// applyLocked is checkAndApply under mu. A write outside a hold that left
// for the console becomes an undo entry when record is set (undo.go).
func (s *Supervisor) applyLocked(w *Write, record bool) (Result, error) {
	before := s.undoBaseLocked(w.Param)
	sess, res, err := s.prepareLocked(w)
	if err != nil || !res.Written {
		return res, err
	}
	if err := sess.Apply(w.Command); err != nil {
		if w.Param.Field == model.FieldGain {
			s.safe.forgetSent(w.Param, dbOf(w.Value)) // never sent: not in flight (re-review MEDIUM A)
		}
		return Result{}, err
	}
	s.safe.noteSeatSent(w.Origin, w.Param, dbOf(w.Value), s.sameLevelLocked, time.Now())
	if iw := s.safe.windows[holdKey{param: w.Param, client: w.Origin.ClientID}]; iw != nil {
		iw.lastWrite, iw.wrote = dbOf(w.Value), true // the next write of the hold is bounded against it (CC-3)
	} else if record {
		s.recordLocked(w.Origin, w.Param, before, w.Value, !w.Typed && undoKindOf(w.Param.Field) == UndoLevel)
	}
	return res, nil
}

// undoBaseLocked is the value an undo entry of p starts from: the console
// value, for Gain the newest value of ours in flight (where it is going).
func (s *Supervisor) undoBaseLocked(p model.ParamRef) model.Value {
	if p.Field == model.FieldGain {
		if v, ok := s.safe.newestInFlight(p); ok {
			if lv, err := model.Level(v); err == nil {
				return lv
			}
		}
	}
	v, _ := s.state.Value(p)
	return v
}

// sameLevelLocked reports whether levels a and b of p are one console
// value: within half a step of p's grid (send levels 160 steps, faders
// 1023, capabilities) in fader positions; exactly equal without a grid.
func (s *Supervisor) sameLevelLocked(p model.ParamRef, a, b float64) bool {
	steps, law := s.caps.FaderSteps, s.cfg.Driver.FaderLaw()
	if p.Field == model.FieldSendLevel {
		steps = s.caps.SendLevelSteps
	}
	if steps <= 0 || law == nil {
		return a == b
	}
	return math.Abs(law.PositionFromDB(a)-law.PositionFromDB(b)) <= 0.5/float64(steps)+1e-9
}

// gateLocked refuses a write before any value rule: the client's role
// (first: below every UI, invariant 4), not live (rule 3), a read-only
// parameter, one this console does not have (never faked).
func (s *Supervisor) gateLocked(w *Write) error {
	if err := s.checkRoleLocked(w.Param, w.Origin); err != nil {
		return err
	}
	if s.status != StatusLive || s.sess == nil || s.limited != "" {
		return driver.ErrStale
	}
	if s.readOnlyLocked(w.Param) {
		return fmt.Errorf("console: %s is read-only: %w", w.Param, driver.ErrUnsupported)
	}
	if _, ok := s.state.Value(w.Param); !ok && w.Param.Strip.Kind != model.StripHeadamp {
		return fmt.Errorf("console: %s: not on this console: %w", w.Param, driver.ErrUnsupported)
	}
	return nil
}

// prepareLocked checks a write and sets w.Value to what may be written.
func (s *Supervisor) prepareLocked(w *Write) (driver.Session, Result, error) {
	if err := s.gateLocked(w); err != nil {
		return nil, Result{}, err
	}
	k := holdKey{param: w.Param, client: w.Origin.ClientID}
	if s.holds.isEnded(w.Param, w.Origin.ClientID) {
		return nil, Result{}, ErrGestureEnded
	}
	if _, held := s.holds.active[k]; !held {
		s.closeWindowLocked(k)
	}
	if !isLevel(w.Param.Field) || w.Value.Kind() != model.KindLevel {
		return s.sess, Result{Value: w.Value, Written: true}, nil
	}
	curV, ok := s.state.Value(w.Param)
	if !ok {
		return s.sess, Result{Value: w.Value, Written: true}, nil // the session refuses unknown parameters
	}
	cur := dbOf(curV)
	v, st, win, err := s.clampLocked(*w, cur)
	if errors.Is(err, errEmptyGainBound) { // write nothing; the hold stays open until a read-back settles it
		start, _ := model.Level(win.Start)
		return s.sess, Result{Value: start, Window: win}, nil
	}
	if err != nil {
		return nil, Result{}, err
	}
	lv, err := model.Level(v)
	if err != nil {
		return nil, Result{}, err
	}
	w.Value = lv
	written := v != cur
	if w.Param.Field == model.FieldGain {
		others := slices.ContainsFunc(s.safe.inFlightOf(w.Param), func(x float64) bool { return x != v })
		written = written || others // back to the console value while others are in flight: a real write
		if written {
			// Inside a hold the count never refuses (CC-3): the drag is bounded
			// write by write against its previous write instead.
			send, ok := s.safe.noteSent(w.Param, v, time.Now(), win != nil)
			if !ok {
				return nil, Result{}, fmt.Errorf("%w: %s: %d values in flight", ErrGainBound, w.Param, maxInFlight)
			}
			written = send // sent again at most once per echo window (LOW 5)
		}
	}
	return s.sess, Result{Value: lv, State: st, Written: written, Window: win}, nil
}

// readOnlyLocked reports a parameter the core never writes, whatever the
// value: headamp phantom, an input's headamp, the Gain of a headamp whose
// law or ownership is unconfirmed (capabilities writable false) and, in
// core-api v0.4, send taps, colours, bus links and matrices.
func (s *Supervisor) readOnlyLocked(p model.ParamRef) bool {
	switch {
	case p.Field == model.FieldPhantom || p.Field == model.FieldHeadamp:
		return true
	case p.Field == model.FieldSendTap || p.Field == model.FieldColour || p.Field == model.FieldLinked || p.Strip.Kind == model.StripMatrix:
		return true
	case p.Strip.Kind != model.StripHeadamp:
		return false
	}
	info, ok := s.caps.Headamp(p.Strip.Index)
	return !ok || !info.Writable
}

// Hold starts (held = true) or ends (false) a person's gesture on a level
// parameter (a fader or Gain). See HoldWindow.
func (s *Supervisor) Hold(p model.ParamRef, origin driver.Origin, held bool) error {
	_, err := s.HoldWindow(p, origin, held)
	return err
}

// HoldWindow starts or ends a gesture and returns its window (CR-22). Both
// clear an ended gesture of that client. Holding never writes. A hold can
// only begin while live (ErrStale otherwise), so no finger held through an
// outage writes after the reconnect; ending a hold always works.
func (s *Supervisor) HoldWindow(p model.ParamRef, origin driver.Origin, held bool) (Result, error) {
	if !origin.MayWrite() {
		return Result{}, driver.ErrNotUserOrigin
	}
	if !isLevel(p.Field) {
		return Result{}, fmt.Errorf("console: hold %s: %w", p, driver.ErrUnsupported)
	}
	k := holdKey{param: p, client: origin.ClientID}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.syncHeldLocked()
	if held {
		if err := s.checkRoleLocked(p, origin); err != nil {
			return Result{}, err
		}
	}
	if held && (s.status != StatusLive || s.limited != "") {
		return Result{}, driver.ErrStale
	}
	if held && s.readOnlyLocked(p) {
		return Result{}, fmt.Errorf("console: hold %s: read-only: %w", p, driver.ErrUnsupported)
	}
	delete(s.holds.ended, k)
	s.closeWindowLocked(k) // a hold that wrote is one undo entry (CC-6)
	if !held {
		delete(s.holds.active, k)
		if isHeadampGain(p) {
			s.safe.unlocks.touch(p.Strip.Index) // the idle 20 s start again at release (CC-4)
		}
		return Result{}, nil
	}
	cur, ok := s.state.Value(p)
	if !ok {
		return Result{}, fmt.Errorf("console: hold %s: %w", p, model.ErrUnknownParam)
	}
	s.endOtherGainHoldsLocked(p, origin) // one finger per preamp (review blocker 1)
	s.holds.active[k] = struct{}{}
	if isHeadampGain(p) {
		s.safe.unlocks.touch(p.Strip.Index)
	}
	w := s.openWindowLocked(k)
	if isHeadampGain(p) {
		cur, _ = model.Level(w.Start) // where the console is going (CC-3); finite
	}
	return Result{Value: cur, Window: windowOf(w)}, nil
}
