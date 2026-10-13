// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Show mode unlock timing (SR-27). Design values.
const (
	unlockIdle = 20 * time.Second // an unlock ends after 20 s without a command on it
	undoGrace  = 10 * time.Second // an undo of an unlock entry stays allowed this long after the relock
	maxUndoTo  = 32               // values before entries remembered per unlock (the first ones are kept)
)

// Unlock end reasons (state event gainUnlockEnded).
const (
	UnlockRelock      = "relock"
	UnlockIdle        = "idle"
	UnlockStale       = "stale"
	UnlockStopped     = "stopped"
	UnlockShowModeOff = "showModeOff"
)

// UnlockEvent reports a Gain unlock that ended (SR-27).
type UnlockEvent struct {
	Headamp int
	Reason  string
}

type unlockEntry struct {
	last   time.Time // last command on the headamp
	undoTo []float64 // Gain before each entry of this unlock (dB, distinct)
	timer  *time.Timer
}

// relocked is the undo grace of an unlock that made entries.
type relocked struct {
	at     time.Time
	undoTo []float64
}

// unlocks are the active Gain unlocks of show mode. Guarded by Supervisor.mu.
type unlocks struct {
	active   map[int]*unlockEntry
	relocked map[int]relocked // per headamp: the last unlock that made an entry
	events   []UnlockEvent
}

func newUnlocks() unlocks {
	return unlocks{active: map[int]*unlockEntry{}, relocked: map[int]relocked{}}
}

func (u *unlocks) touch(h int) {
	if e, ok := u.active[h]; ok {
		e.last = time.Now()
	}
}

// noteEntry records an accepted Gain write during an unlock and the value
// Gain had before it, the only value an undo may return to after the relock.
func (u *unlocks) noteEntry(h int, before, tol float64) {
	e, ok := u.active[h]
	if !ok || len(e.undoTo) >= maxUndoTo || slices.ContainsFunc(e.undoTo, near(before, tol)) {
		return
	}
	e.undoTo = append(slices.Clip(e.undoTo), before)
}

// allowsRaise: the headamp is unlocked, or this is an undo within the grace
// after the relock, back to a value Gain had before an entry of that unlock.
func (u *unlocks) allowsRaise(h int, undo bool, v, tol float64) bool {
	if _, ok := u.active[h]; ok {
		return true
	}
	r, ok := u.relocked[h]
	return undo && ok && time.Since(r.at) <= undoGrace && slices.ContainsFunc(r.undoTo, near(v, tol))
}

// near matches a Gain value within tol (half a step of the headamp law).
func near(v, tol float64) func(float64) bool {
	return func(x float64) bool { return math.Abs(x-v) <= tol }
}

func (u *unlocks) end(h int, reason string) {
	e, ok := u.active[h]
	if !ok {
		return
	}
	e.timer.Stop()
	delete(u.active, h)
	if len(e.undoTo) > 0 {
		u.relocked[h] = relocked{at: time.Now(), undoTo: e.undoTo}
	}
	if len(u.events) < maxPendingEvents {
		u.events = append(u.events, UnlockEvent{Headamp: h, Reason: reason})
	}
}

func (u *unlocks) endAll(reason string) {
	for _, h := range slices.Sorted(maps.Keys(u.active)) {
		u.end(h, reason)
	}
}

func (u *unlocks) list() []int { return slices.Sorted(maps.Keys(u.active)) }

func isHeadampGain(p model.ParamRef) bool {
	return p.Strip.Kind == model.StripHeadamp && p.Field == model.FieldGain
}

// SetShowMode turns show mode on or off (SR-27). It writes nothing; turning
// it off ends every unlock.
//
// Unchecked and owner-only (review Sec L5): it checks no role. A hub or an
// API acting for a client uses SetShowModeBy, which refuses monitor and
// viewer roles (ERR_ROLE); a hub never exposes SetShowMode itself.
func (s *Supervisor) SetShowMode(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.safe.showMode = on
	if !on {
		s.safe.unlocks.endAll(UnlockShowModeOff)
	}
	s.signal()
}

// Unlock allows raising Gain on one headamp in show mode after the UI's
// one-tap Adjust (SR-27 amended, no minimum hold), until Relock, 20 s
// without a command on it (paused while a Gain hold on it is open, CC-4),
// stale, pause or stop. It writes nothing. Outside show mode it does nothing.
func (s *Supervisor) Unlock(p model.ParamRef, origin driver.Origin) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if !isHeadampGain(p) {
		return fmt.Errorf("console: unlock %s: %w", p, driver.ErrUnsupported)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkConsoleRoleLocked(origin); err != nil {
		return err
	}
	if s.status != StatusLive || s.limited != "" {
		return driver.ErrStale
	}
	if _, ok := s.state.Value(p); !ok {
		return fmt.Errorf("console: unlock %s: %w", p, model.ErrUnknownParam)
	}
	if info, ok := s.caps.Headamp(p.Strip.Index); !ok || !info.Writable {
		return fmt.Errorf("console: unlock %s: read-only headamp: %w", p, driver.ErrUnsupported)
	}
	h := p.Strip.Index
	if !s.safe.showMode || s.safe.unlocks.active[h] != nil {
		s.safe.unlocks.touch(h)
		return nil
	}
	e := &unlockEntry{last: time.Now()}
	e.timer = time.AfterFunc(unlockIdle, func() { s.unlockIdleCheck(h, e) })
	s.safe.unlocks.active[h] = e
	s.signal()
	return nil
}

// unlockIdleCheck ends an unlock that had no command for unlockIdle, or
// re-arms its timer for the rest of the time.
func (s *Supervisor) unlockIdleCheck(h int, e *unlockEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.safe.unlocks.active[h] != e {
		return // ended meanwhile
	}
	if s.gainHeldLocked(h) { // a finger on the wheel: the 20 s never run out (CC-4)
		e.last = time.Now()
	}
	if left := unlockIdle - time.Since(e.last); left > 0 {
		e.timer.Reset(left)
		return
	}
	s.safe.unlocks.end(h, UnlockIdle)
	s.signal()
}

// gainHeldLocked reports an open hold on headamp h's Gain. Callers hold mu.
func (s *Supervisor) gainHeldLocked(h int) bool {
	for k := range s.holds.active {
		if isHeadampGain(k.param) && k.param.Strip.Index == h {
			return true
		}
	}
	return false
}

// Relock ends the Gain unlock of one headamp (the sheet closed). It writes nothing.
func (s *Supervisor) Relock(p model.ParamRef, origin driver.Origin) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if !isHeadampGain(p) {
		return fmt.Errorf("console: relock %s: %w", p, driver.ErrUnsupported)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkConsoleRoleLocked(origin); err != nil {
		return err
	}
	s.safe.unlocks.end(p.Strip.Index, UnlockRelock)
	s.signal()
	return nil
}
