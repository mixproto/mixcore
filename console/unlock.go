// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Show mode unlock timing (SR-27). Design values.
const (
	unlockIdle = 20 * time.Second // an unlock ends after 20 s without a command on it
	undoGrace  = 10 * time.Second // an undo of an unlock entry stays allowed this long after the relock
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
	last    time.Time // last command on the headamp
	entries bool      // a Gain value changed during the unlock
	timer   *time.Timer
}

// unlocks are the active Gain unlocks of show mode. Guarded by Supervisor.mu.
type unlocks struct {
	active     map[int]*unlockEntry
	relockedAt map[int]time.Time // relock time of unlocks that made an entry (undo grace)
	events     []UnlockEvent
}

func newUnlocks() unlocks {
	return unlocks{active: map[int]*unlockEntry{}, relockedAt: map[int]time.Time{}}
}

func (u *unlocks) touch(h int) {
	if e, ok := u.active[h]; ok {
		e.last = time.Now()
	}
}

func (u *unlocks) noteEntry(h int) {
	if e, ok := u.active[h]; ok {
		e.entries = true
	}
}

// allowsRaise: the headamp is unlocked, or this is an undo within the grace
// after the relock of an unlock that made an entry.
func (u *unlocks) allowsRaise(h int, undo bool) bool {
	if _, ok := u.active[h]; ok {
		return true
	}
	at, ok := u.relockedAt[h]
	return undo && ok && time.Since(at) <= undoGrace
}

func (u *unlocks) end(h int, reason string) {
	e, ok := u.active[h]
	if !ok {
		return
	}
	e.timer.Stop()
	delete(u.active, h)
	if e.entries {
		u.relockedAt[h] = time.Now()
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
// 1 s hold (SR-27), until Relock, 20 s without a command on it, stale,
// pause or stop. It writes nothing. Outside show mode it does nothing.
func (s *Supervisor) Unlock(p model.ParamRef, origin driver.Origin) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if !isHeadampGain(p) {
		return fmt.Errorf("console: unlock %s: %w", p, driver.ErrUnsupported)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != StatusLive || s.limited != "" {
		return driver.ErrStale
	}
	if _, ok := s.state.Value(p); !ok {
		return fmt.Errorf("console: unlock %s: %w", p, model.ErrUnknownParam)
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
	if left := unlockIdle - time.Since(e.last); left > 0 {
		e.timer.Reset(left)
		return
	}
	s.safe.unlocks.end(h, UnlockIdle)
	s.signal()
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
	s.safe.unlocks.end(p.Strip.Index, UnlockRelock)
	s.signal()
	return nil
}
