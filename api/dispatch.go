// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/internal/safe"
)

// startDispatcher runs the single goroutine that calls the StateListener.
// Callers hold e.mu.
func (e *Engine) startDispatcher(sup *console.Supervisor) {
	ctx, cancel := context.WithCancel(context.Background())
	e.stopDisp = cancel
	e.wantFull.Store(true)
	onPanic := func(err error) { e.opts.Logger.Error("api: state listener panicked", "err", err) }
	safe.Go(&e.dispWG, onPanic, func() { e.dispatch(ctx, sup) })
}

// dispatch delivers supervisor updates as JSON. A slow listener only delays
// this goroutine; the supervisor coalesces meanwhile. The final state after
// Stop is delivered before the goroutine ends.
func (e *Engine) dispatch(ctx context.Context, sup *console.Supervisor) {
	var seq uint64
	for {
		select {
		case <-ctx.Done():
			e.deliver(sup, &seq)
			return
		case <-sup.Notify():
		case <-e.kick:
		}
		e.deliver(sup, &seq)
	}
}

func (e *Engine) deliver(sup *console.Supervisor, seq *uint64) {
	u := sup.Drain()
	full := u.Full || e.wantFull.Swap(false)
	if !full && len(u.Changes) == 0 && len(u.Events) == 0 {
		return
	}
	box := e.lis.Load()
	if box == nil {
		e.wantFull.Store(true) // whoever listens next starts from a snapshot
		return
	}
	*seq++
	b, err := encodeUpdate(u, *seq, full)
	if err != nil {
		e.opts.Logger.Error("api: encode state", "err", err)
		return
	}
	e.callListener(box.l, string(b))
}

// callListener contains a panic in app code: the dispatcher keeps running.
func (e *Engine) callListener(l StateListener, msg string) {
	defer func() {
		if r := recover(); r != nil {
			e.opts.Logger.Error("api: state listener panicked", "panic", r)
		}
	}()
	l.OnState(msg)
}
