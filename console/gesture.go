// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"

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
	sess, res, err := s.prepare(&w)
	if err != nil || !res.Written {
		return res, err
	}
	err = sess.Apply(w.Command)
	if errors.Is(err, driver.ErrClosed) {
		return Result{}, driver.ErrStale // the session died between the check and the call
	}
	return res, err
}

// prepare checks a write under mu (released even if a rule panics).
func (s *Supervisor) prepare(w *Write) (driver.Session, Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareLocked(w)
}

// prepareLocked checks a write and sets w.Value to what may be written.
func (s *Supervisor) prepareLocked(w *Write) (driver.Session, Result, error) {
	if s.status != StatusLive || s.sess == nil || s.limited != "" {
		return nil, Result{}, driver.ErrStale
	}
	k := holdKey{param: w.Param, client: w.Origin.ClientID}
	if s.holds.isEnded(w.Param, w.Origin.ClientID) {
		return nil, Result{}, ErrGestureEnded
	}
	if _, held := s.holds.active[k]; !held {
		delete(s.safe.windows, k)
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
		if pend, hasPend := s.safe.pendingOf(w.Param); hasPend {
			written = v != pend // the last value sent is the one in flight
		}
		if written {
			s.safe.notePending(w.Param, v)
		}
	}
	return s.sess, Result{Value: lv, State: st, Written: written, Window: win}, nil
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
	if held && (s.status != StatusLive || s.limited != "") {
		return Result{}, driver.ErrStale
	}
	delete(s.holds.ended, k)
	delete(s.safe.windows, k)
	if !held {
		delete(s.holds.active, k)
		return Result{}, nil
	}
	cur, ok := s.state.Value(p)
	if !ok {
		return Result{}, fmt.Errorf("console: hold %s: %w", p, model.ErrUnknownParam)
	}
	s.holds.active[k] = struct{}{}
	if isHeadampGain(p) {
		s.safe.unlocks.touch(p.Strip.Index)
	}
	return Result{Value: cur, Window: windowOf(s.openWindowLocked(k))}, nil
}

// OpenWall re-opens a slip-guard wall after the UI measured a 300 ms rest
// there (CR-22, SR-26). It is accepted only while the last candidate stopped
// at the wall; the next upward move then continues from the wall value and
// the next wall is 6 dB higher. Gain caps and monitor ceilings never open
// (ErrUnsupported). It writes nothing.
func (s *Supervisor) OpenWall(p model.ParamRef, origin driver.Origin) (Result, error) {
	if !origin.MayWrite() {
		return Result{}, driver.ErrNotUserOrigin
	}
	k := holdKey{param: p, client: origin.ClientID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holds.isEnded(p, origin.ClientID) {
		return Result{}, ErrGestureEnded
	}
	w, held := s.safe.windows[k]
	if _, active := s.holds.active[k]; !active || !held {
		return Result{}, fmt.Errorf("console: open wall %s without a hold: %w", p, driver.ErrUnsupported)
	}
	if !w.OpensByRest {
		return Result{}, fmt.Errorf("console: %s never opens by rest: %w", p, driver.ErrUnsupported)
	}
	cur, _ := s.state.Value(p)
	if !w.openWall() {
		return Result{Value: cur, State: w.state, Window: windowOf(w)}, nil
	}
	return Result{Value: cur, State: StateFree, Window: windowOf(w)}, nil
}
