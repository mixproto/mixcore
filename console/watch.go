// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Watch (redesign CC-11, S7, core-api v0.5 rule 15): a client names the bus
// it shows (sends mode, bus detail, monitor view); a change in that scope
// that the client did not make is reported as a ScopeEvent, the first at
// once, then at most once per scopeCoalesce while changes continue, so the
// UI can say "Mon 2 changed at the mixer". It never writes.

const (
	scopeCoalesce = 2 * time.Second // fader-and-gain.md 15, CC-11
	maxWatches    = 64              // clients with a watch; the oldest watch is dropped
)

// ScopeEvent reports changes in a client's watched bus since its last one.
type ScopeEvent struct {
	ClientID string
	Bus      int
	Count    int
}

type watcher struct {
	bus      int
	since    time.Time // when the watch was set
	pending  int       // changes not reported yet
	lastEmit time.Time
	timer    *time.Timer // flushes pending changes after the coalesce window
}

// Watch sets the bus client shows (bus 0 clears). Any role may watch: it
// only reads.
func (s *Supervisor) Watch(origin driver.Origin, bus int) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.watches[origin.ClientID]; old != nil {
		stopTimer(old.timer)
		delete(s.watches, origin.ClientID)
	}
	if bus <= 0 {
		return nil
	}
	if len(s.watches) >= maxWatches {
		s.dropOldestWatchLocked()
	}
	s.watches[origin.ClientID] = &watcher{bus: bus, since: time.Now()}
	return nil
}

func (s *Supervisor) dropOldestWatchLocked() {
	oldest := ""
	for c, w := range s.watches {
		if oldest == "" || w.since.Before(s.watches[oldest].since) {
			oldest = c
		}
	}
	stopTimer(s.watches[oldest].timer)
	delete(s.watches, oldest)
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

// noteScopeChangeLocked counts a reported change c (a value that moved,
// never our own read-back) for every watch whose scope holds it and whose
// client did not make it. Callers hold mu.
func (s *Supervisor) noteScopeChangeLocked(c Change) {
	for client, w := range s.watches {
		if isOwnEcho(c.Origin, client) || !s.inScopeLocked(c.Param, w.bus) {
			continue
		}
		w.pending++
		s.emitScopeLocked(client, w, time.Now())
	}
}

// emitScopeLocked reports a watch's pending changes now, or once the
// coalesce window since its last report has passed.
func (s *Supervisor) emitScopeLocked(client string, w *watcher, now time.Time) {
	if w.timer != nil || w.pending == 0 {
		return
	}
	if wait := scopeCoalesce - now.Sub(w.lastEmit); !w.lastEmit.IsZero() && wait > 0 {
		w.timer = time.AfterFunc(wait, func() { s.flushWatch(client, w) })
		return
	}
	if len(s.scopes) < maxPendingEvents {
		s.scopes = append(s.scopes, ScopeEvent{ClientID: client, Bus: w.bus, Count: w.pending})
	}
	w.pending, w.lastEmit = 0, now
	s.signal()
}

// flushWatch reports what accumulated during a coalesce window.
func (s *Supervisor) flushWatch(client string, w *watcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches[client] != w {
		return // cleared or replaced meanwhile
	}
	w.timer = nil
	s.emitScopeLocked(client, w, time.Now())
}

// inScopeLocked: p is a send into bus b, a field of bus b, or the same of
// its partner while the pair is linked (one stereo mix).
func (s *Supervisor) inScopeLocked(p model.ParamRef, b int) bool {
	busOf := func(p model.ParamRef) int {
		switch {
		case p.Field.IsSend():
			return p.Bus
		case p.Strip.Kind == model.StripBus:
			return p.Strip.Index
		}
		return 0
	}
	pb := busOf(p)
	if pb == 0 {
		return false
	}
	if pb == b {
		return true
	}
	odd := b - (1 - b%2)
	if pb != odd && pb != odd+1 {
		return false
	}
	info, ok := s.busInfoLocked(odd)
	linked, _ := s.state.Value(model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: odd}, Field: model.FieldLinked})
	on, _ := linked.Bool()
	return ok && info.Linkable && on
}
