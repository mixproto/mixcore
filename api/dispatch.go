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

// startDispatcher runs the single goroutine that calls the StateListener.
// Callers hold e.mu.
func (e *Engine) startDispatcher(sup *console.Supervisor) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup // this session's dispatcher only: stopping it never waits for the next session's
	e.stopDisp = func() { cancel(); wg.Wait() }
	e.wantFull.Store(true)
	gen := e.dispGen.Add(1)
	onPanic := func(err error) { e.opts.Logger.Error("api: state listener panicked", "err", err) }
	safe.Go(&wg, onPanic, func() { e.dispatch(ctx, sup, gen) })
}

// dispatch delivers supervisor updates as JSON. A slow listener only delays
// this goroutine; the supervisor coalesces meanwhile. The final state after
// Stop is delivered before the goroutine ends, unless a newer session
// already owns the listener (Stop and Start racing on two threads).
func (e *Engine) dispatch(ctx context.Context, sup *console.Supervisor, gen uint64) {
	var seq uint64
	for {
		select {
		case <-ctx.Done():
			e.deliver(sup, &seq, gen)
			return
		case <-sup.Notify():
		case <-e.kick:
		}
		e.deliver(sup, &seq, gen)
	}
}

// deliver runs under deliverMu: a stopping session's dispatcher and the next
// session's never call OnState at the same time, and only the newest
// session (gen) reaches the listener.
func (e *Engine) deliver(sup *console.Supervisor, seq *uint64, gen uint64) {
	e.deliverMu.Lock()
	defer e.deliverMu.Unlock()
	if gen != e.dispGen.Load() {
		return
	}
	u := sup.Drain()
	full := u.Full || e.wantFull.Swap(false)
	safetyMoved := e.safetyChanged(u)
	if !full && len(u.Changes) == 0 && len(u.Effective) == 0 && len(u.Events) == 0 && len(u.Scenes) == 0 &&
		len(u.UnlockEnded) == 0 && !safetyMoved {
		return
	}
	box := e.lis.Load()
	if box == nil {
		e.wantFull.Store(true) // whoever listens next starts from a snapshot
		return
	}
	*seq++
	b, err := encodeUpdate(u, *seq, full, e.demo.session.Load())
	if err != nil {
		e.opts.Logger.Error("api: encode state", "err", err)
		return
	}
	e.callListener(box.l, string(b))
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

// safetyChanged reports a change of show mode or the unlocked headamps since
// the last delivered message (they travel in every message, v0.3).
func (e *Engine) safetyChanged(u console.Update) bool {
	key := fmt.Sprint(u.ShowMode, u.Unlocked)
	if key == e.lastSafety {
		return false
	}
	e.lastSafety = key
	return true
}
