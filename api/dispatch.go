// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"fmt"
	"sync"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/internal/safe"
)

// dispatchSession is what one session's dispatcher delivers for: its
// supervisor, its generation and its own demo flag (never the engine's,
// which a racing Start may already have changed).
type dispatchSession struct {
	sup  *console.Supervisor
	gen  uint64
	demo bool
}

// startDispatcher runs the single goroutine that calls the StateListener.
// Callers hold e.mu.
func (e *Engine) startDispatcher(sup *console.Supervisor, isDemo bool) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup // this session's dispatcher only: stopping it never waits for the next session's
	e.stopDisp = func() { cancel(); wg.Wait() }
	e.wantFull.Store(true)
	s := dispatchSession{sup: sup, gen: e.dispGen.Add(1), demo: isDemo}
	onPanic := func(err error) { e.opts.Logger.Error("api: state listener panicked", "err", err) }
	safe.Go(&wg, onPanic, func() { e.dispatch(ctx, s) })
}

// dispatch delivers supervisor updates as JSON. A slow listener only delays
// this goroutine; the supervisor coalesces meanwhile. The final state after
// Stop is delivered before the goroutine ends, unless a newer session
// already owns the listener (Stop and Start racing on two threads).
func (e *Engine) dispatch(ctx context.Context, s dispatchSession) {
	var seq uint64
	for {
		select {
		case <-ctx.Done():
			e.deliver(s, &seq)
			return
		case <-s.sup.Notify():
		case <-e.kick:
		}
		e.deliver(s, &seq)
	}
}

// deliver runs under deliverMu: a stopping session's dispatcher and the next
// session's never call OnState at the same time, and only the newest
// session (gen) reaches the listener.
func (e *Engine) deliver(s dispatchSession, seq *uint64) {
	e.deliverMu.Lock()
	defer e.deliverMu.Unlock()
	if s.gen != e.dispGen.Load() {
		return
	}
	if hook := e.afterGenCheck.Load(); hook != nil {
		(*hook)()
	}
	u := s.sup.Drain()
	full := u.Full || e.wantFull.Swap(false)
	safetyMoved := e.safetyChanged(u)
	favMoved := e.favouritesChanged(u)
	if !full && len(u.Changes) == 0 && len(u.Effective) == 0 && len(u.Events) == 0 && len(u.Scenes) == 0 &&
		len(u.UnlockEnded) == 0 && len(u.Scopes) == 0 && !safetyMoved && !favMoved {
		return
	}
	box := e.lis.Load()
	if box == nil {
		e.wantFull.Store(true) // whoever listens next starts from a snapshot
		return
	}
	*seq++
	b, err := encodeUpdateWith(u, *seq, full, s.demo, favMoved)
	if err != nil {
		e.opts.Logger.Error("api: encode state", "err", err)
		return
	}
	e.callListener(box.l, string(b))
}

// favouritesChanged reports a change of the favourites since the last
// delivered message: the next message carries them (v0.5).
func (e *Engine) favouritesChanged(u console.Update) bool {
	key := favouritesKey(u)
	if key == e.lastFavourites {
		return false
	}
	e.lastFavourites = key
	return true
}

// callListener contains a panic in app code: the dispatcher keeps running.
func (e *Engine) callListener(l StateListener, msg string) {
	e.onStateGoroutine.Store(goroutineID()) // calls back into Stop, Pause, StopDemo get ERR_BUSY
	defer e.onStateGoroutine.Store(0)
	defer func() {
		if r := recover(); r != nil {
			e.opts.Logger.Error("api: state listener panicked", "panic", r)
		}
	}()
	l.OnState(msg)
}

// safetyChanged reports a change of show mode, the unlocked headamps or
// (v0.5) the PIN lock and undo entries since the last delivered message
// (they travel in every message).
func (e *Engine) safetyChanged(u console.Update) bool {
	key := fmt.Sprint(u.ShowMode, u.Unlocked) + v05Key(u)
	if key == e.lastSafety {
		return false
	}
	e.lastSafety = key
	return true
}
