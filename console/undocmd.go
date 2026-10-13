// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Errors of the undo commands (core-api v0.5 rule 14).
var (
	// ErrNothingToUndo: the client has no history (wraps driver.ErrUnsupported).
	ErrNothingToUndo = fmt.Errorf("console: nothing to undo: %w", driver.ErrUnsupported)
	// ErrUndoHeld: the client still holds the parameter its newest entry
	// changed; cancelHold is the way (ERR_INVALID_COMMAND).
	ErrUndoHeld = errors.New("console: the parameter is held; cancel the hold instead")
)

// undoAsksLocked lists what undoing e would ask now, in the contract's order.
func (s *Supervisor) undoAsksLocked(e undoEntry, now time.Time) []string {
	cur := e.to // nothing else changed it: the console is where the entry left it
	if e.foreign {
		cur = s.undoBaseLocked(e.param) // for Gain where the console is going
	}
	ok := cur.Kind() != 0
	var asks []string
	add := func(on bool, ask string) {
		if on {
			asks = append(asks, ask)
		}
	}
	add(undoUnmutes(e), AskUnmute)
	if e.kind == UndoLevel && ok {
		from, now, to := dbOf(e.from), dbOf(cur), dbOf(e.to)
		raise := from > now && !s.sameValueLocked(e.param, e.from, cur)
		add(raise && from-now > undoRaiseAskDB, AskRaiseOver6dB)
		add(raise && to < from && time.Since(e.at) < recentLowerAsk, AskRaiseAfterRecentLower)
		add(e.param.Field == model.FieldGain && math.Abs(from-now) > undoGainBoundDB, AskGainOver6dB)
	}
	add(e.foreign && ok && !s.sameValueLocked(e.param, e.to, cur), AskConflict)
	add(e.cut != nil, AskBeforeSceneRecall)
	return orderAsks(asks)
}

// undoUnmutes: the undo turns a mute off (a strip's own, a mute group, a
// DCA) or a send on: audio may come back. It asks whatever the strip's
// effective mute is, also when the console leaves it unknown (wing: muted
// null with an engaged mute group of unknown membership), so an undo never
// unmutes without asking.
func undoUnmutes(e undoEntry) bool {
	on, _ := e.from.Bool()
	return (e.param.Field == model.FieldMute && !on) || (e.param.Field == model.FieldSendOn && on)
}

var askOrder = []string{AskUnmute, AskRaiseOver6dB, AskRaiseAfterRecentLower, AskConflict, AskBeforeSceneRecall, AskGainOver6dB}

func orderAsks(asks []string) []string {
	out := make([]string, 0, len(asks))
	for _, a := range askOrder {
		for _, b := range asks {
			if a == b {
				out = append(out, a)
			}
		}
	}
	return out
}

// Undo writes the from value of the client's newest entry once and removes
// the entry (core-api v0.5 rule 14). When the undo asks, it writes nothing
// and returns ErrNeedsConfirm unless confirmed (a person's tap: origin
// gesture). A confirmed undo is exempt from the Gain bound like confirmed
// typed entry; roles, stale, the monitor ceiling and show mode still apply.
func (s *Supervisor) Undo(origin driver.Origin, confirmed bool) (Result, error) {
	if !origin.MayWrite() {
		return Result{}, driver.ErrNotUserOrigin
	}
	if confirmed && origin.Kind != driver.OriginGesture {
		return Result{}, ErrTypedOrigin
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.undo.newest(origin.ClientID)
	if !ok {
		return Result{}, ErrNothingToUndo
	}
	if _, held := s.holds.active[holdKey{param: e.param, client: origin.ClientID}]; held {
		return Result{}, ErrUndoHeld
	}
	if asks := s.undoAsksLocked(e, time.Now()); len(asks) > 0 && !confirmed {
		return Result{}, fmt.Errorf("%w: undo of %s asks %v", ErrNeedsConfirm, e.param, asks)
	}
	level := e.kind == UndoLevel
	w := Write{Command: driver.Command{Param: e.param, Value: e.from, Origin: origin}, Undo: true, Typed: confirmed && level, Confirmed: confirmed && level}
	res, err := s.applyLocked(&w, false)
	if errors.Is(err, driver.ErrClosed) {
		err = driver.ErrStale
	}
	if err != nil {
		return Result{}, err
	}
	s.undo.pop(origin.ClientID)
	s.signal()
	return res, nil
}

// CancelHold ends the client's open hold on p and, if it wrote, writes its
// grab value once (redesign CC-9, AC-RD-U04): exempt from the window and
// show mode (it is never above the value the console had at holdBegin);
// for Gain within 6 dB of the hold's last write (else ErrGainBound and the
// hold stays open). The hold ends like an external change, so a further
// set is ErrGestureEnded until a new hold-begin or hold-end. No undo entry.
func (s *Supervisor) CancelHold(p model.ParamRef, origin driver.Origin) (Result, error) {
	if !origin.MayWrite() {
		return Result{}, driver.ErrNotUserOrigin
	}
	if !isLevel(p.Field) {
		return Result{}, fmt.Errorf("console: cancel hold %s: %w", p, driver.ErrUnsupported)
	}
	k := holdKey{param: p, client: origin.ClientID}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.syncHeldLocked()
	if err := s.checkRoleLocked(p, origin); err != nil {
		return Result{}, err
	}
	if s.status != StatusLive || s.sess == nil || s.limited != "" {
		return Result{}, driver.ErrStale
	}
	w, held := s.safe.windows[k]
	if _, active := s.holds.active[k]; !active || !held {
		if s.holds.isEnded(p, origin.ClientID) {
			return Result{}, ErrGestureEnded
		}
		return Result{}, fmt.Errorf("console: cancel hold %s: no open hold: %w", p, driver.ErrUnsupported)
	}
	start, _ := model.Level(w.Start) // finite or -inf, never NaN
	res := Result{Value: start, Window: windowOf(w)}
	if w.wrote {
		written, err := s.restoreLocked(p, origin, w)
		if err != nil {
			return Result{}, err
		}
		res.Written = written
	}
	delete(s.holds.active, k)
	delete(s.safe.windows, k) // no history entry (CC-9)
	s.holds.ended[k] = struct{}{}
	if isHeadampGain(p) {
		s.safe.unlocks.touch(p.Strip.Index)
	}
	s.signal()
	return res, nil
}

// restoreLocked writes a hold's start once (CancelHold). Callers hold mu.
func (s *Supervisor) restoreLocked(p model.ParamRef, origin driver.Origin, w *window) (bool, error) {
	v := w.Start
	if p.Field == model.FieldGain {
		if math.Abs(v-w.lastWrite) > gainBoundDB {
			return false, fmt.Errorf("%w: cancel of %s from %.1f to %.1f dB", ErrGainBound, p, w.lastWrite, v)
		}
		send, _ := s.safe.noteSent(p, v, time.Now(), true)
		if !send {
			return false, nil
		}
	}
	lv, err := model.Level(v)
	if err != nil {
		return false, err
	}
	if err := s.sess.Apply(driver.Command{Param: p, Value: lv, Origin: origin}); err != nil {
		if p.Field == model.FieldGain {
			s.safe.forgetSent(p, v)
		}
		if errors.Is(err, driver.ErrClosed) {
			return false, driver.ErrStale
		}
		return false, err
	}
	s.safe.noteSeatSent(origin, p, v, s.sameLevelLocked, time.Now())
	return true, nil
}

// closeWindowLocked ends the window of hold k: a hold that wrote becomes
// one undo entry from its start to its last write. Callers hold mu.
func (s *Supervisor) closeWindowLocked(k holdKey) {
	w, ok := s.safe.windows[k]
	if !ok {
		return
	}
	delete(s.safe.windows, k)
	if !w.wrote {
		return
	}
	from, err1 := model.Level(w.Start)
	to, err2 := model.Level(w.lastWrite)
	if err1 == nil && err2 == nil { // a hold ended without a release (the desk, stale, a role) met a change elsewhere
		s.recordEntryLocked(driver.Origin{Kind: driver.OriginGesture, ClientID: k.client}, k.param, from, to, false, s.holds.isEnded(k.param, k.client))
	}
}

// closeEndedWindowsLocked closes the window of every hold that is no
// longer active (an external change, stale, a role change). Callers hold mu.
func (s *Supervisor) closeEndedWindowsLocked() {
	for k := range s.safe.windows {
		if _, active := s.holds.active[k]; !active {
			s.closeWindowLocked(k)
		}
	}
}
