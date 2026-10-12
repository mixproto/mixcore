// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
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
	sess, res, err := s.prepare(&w)
	if err != nil || !res.Written {
		return res, err
	}
	err = sess.Apply(w.Command)
	if err != nil && w.Param.Field == model.FieldGain {
		s.mu.Lock()
		s.safe.forgetSent(w.Param, dbOf(w.Value)) // never sent: not in flight (re-review MEDIUM A)
		s.mu.Unlock()
	}
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
	if s.readOnlyLocked(w.Param) {
		return nil, Result{}, fmt.Errorf("console: %s is read-only: %w", w.Param, driver.ErrUnsupported)
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
		others := slices.ContainsFunc(s.safe.inFlightOf(w.Param), func(x float64) bool { return x != v })
		written = written || others // back to the console value while others are in flight: a real write
		if written {
			send, ok := s.safe.noteSent(w.Param, v, time.Now())
			if !ok {
				return nil, Result{}, fmt.Errorf("%w: %s: %d values in flight", ErrGainBound, w.Param, maxInFlight)
			}
			written = send // sent again at most once per echo window (LOW 5)
		}
	}
	return s.sess, Result{Value: lv, State: st, Written: written, Window: win}, nil
}

// readOnlyLocked reports a parameter the core never writes, whatever the
// value: headamp phantom, an input's headamp, and the Gain of a headamp
// whose law or ownership is unconfirmed (capabilities writable false).
func (s *Supervisor) readOnlyLocked(p model.ParamRef) bool {
	switch {
	case p.Field == model.FieldPhantom || p.Field == model.FieldHeadamp:
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
	if held && (s.status != StatusLive || s.limited != "") {
		return Result{}, driver.ErrStale
	}
	if held && s.readOnlyLocked(p) {
		return Result{}, fmt.Errorf("console: hold %s: read-only: %w", p, driver.ErrUnsupported)
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
