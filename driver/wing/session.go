// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// errAlreadyRunning is returned by a second concurrent Run.
var errAlreadyRunning = errors.New("wing: session already running")

// errLocked: the console's TCP remote lock is on (WG-F26).
var errLocked = errors.New("wing: the console is locked for remote control (TCP remote lock)")

type pendingWrite struct {
	idx    int
	val    wireValue
	origin driver.Origin
}

// session is one live connection. Apply, SetMeters, Reread and Close may be
// called from any goroutine; everything else runs on the owner goroutine
// (connect, then Run).
type session struct {
	timing  Timing
	link    *link
	ownLink *link // set by Driver.Connect: closing the session closes the link
	c       *conn
	tab     *table // set during connect, immutable afterwards
	absent  []bool // per parameter, set during connect, read-only afterwards
	meterID uint32
	log     *slog.Logger
	audit   func(driver.WriteRecord)

	ident    driver.Identity
	snapshot model.State
	caps     model.Capabilities

	mu      sync.Mutex
	order   []int
	pending map[int]pendingWrite
	rrSkip  map[model.ParamRef]bool // left out of the next pass (RereadExcept)
	held    map[model.ParamRef]bool // left out of every pass while held (SetHeld)

	done        chan struct{}
	cmdReady    chan struct{}
	meterReady  chan struct{}
	rereadReady chan struct{}
	metersOn    atomic.Bool
	locked      atomic.Bool
	silent      atomic.Bool // ConsoleSilent reported: writes refused
	stopped     atomic.Bool
	running     atomic.Bool
	closeOnce   sync.Once

	own ownerState
}

var _ driver.Session = (*session)(nil)

func newSession(l *link, c *conn) *session {
	return &session{
		timing: l.drv.timing, link: l, c: c, meterID: newMeterID(), log: logger(l.opts), audit: l.opts.WriteAudit,
		pending: map[int]pendingWrite{}, done: make(chan struct{}), cmdReady: make(chan struct{}, 1),
		meterReady: make(chan struct{}, 1), rereadReady: make(chan struct{}, 1),
	}
}

func (s *session) Identity() driver.Identity        { return s.ident }
func (s *session) Capabilities() model.Capabilities { return s.caps }
func (s *session) Snapshot() model.State            { return s.snapshot }

// Apply queues one user write; the latest value per parameter wins.
func (s *session) Apply(cmd driver.Command) error {
	if !cmd.Origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	idx, ok := s.tab.byRef[cmd.Param]
	if !ok || s.tab.all[idx].readOnly || s.absent[idx] {
		return fmt.Errorf("wing %s: %w", cmd.Param, driver.ErrUnsupported)
	}
	if s.locked.Load() {
		return fmt.Errorf("wing %s: %w: %w", cmd.Param, errLocked, driver.ErrUnsupported)
	}
	if s.silent.Load() {
		return fmt.Errorf("wing %s: console silent: %w", cmd.Param, driver.ErrStale)
	}
	val, err := toWire(&s.tab.all[idx], cmd.Value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, queued := s.pending[idx]; !queued {
		s.order = append(s.order, idx)
	}
	s.pending[idx] = pendingWrite{idx: idx, val: val, origin: cmd.Origin}
	s.mu.Unlock()
	if s.stopped.Load() { // Run ended meanwhile: never send it, and say so
		s.dropPending()
		return driver.ErrClosed
	}
	signal(s.cmdReady)
	return nil
}

// SetMeters turns the meter stream on or off (wing.md section 6).
func (s *session) SetMeters(req driver.MeterRequest) error {
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	s.metersOn.Store(req.Inputs)
	s.link.meters.switchOn(req.Inputs, s.meterID, s.tab.inputs)
	signal(s.meterReady)
	return nil
}

// Close stops the session: queued writes are dropped, never sent; meters stop
// being published; the TCP connection is closed with a FIN (WG-F22).
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		s.stopped.Store(true)
		s.link.meters.switchOffIf(s.meterID)
		close(s.done)
		s.dropPending()
		s.c.close()
		s.link.release(s)
		if l := s.ownLink; l != nil {
			_ = l.Close()
		}
	})
	return nil
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *session) dropPending() {
	s.mu.Lock()
	s.order, s.pending = nil, map[int]pendingWrite{}
	s.mu.Unlock()
}

func (s *session) takePending() []pendingWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pendingWrite, 0, len(s.order))
	for _, idx := range s.order {
		out = append(out, s.pending[idx])
	}
	s.order, s.pending = s.order[:0], map[int]pendingWrite{}
	return out
}

// Run owns the connection until ctx is cancelled or the console is lost.
func (s *session) Run(ctx context.Context, events chan<- driver.Event) error {
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	if !s.running.CompareAndSwap(false, true) {
		return errAlreadyRunning
	}
	defer func() { s.stopped.Store(true); s.dropPending() }() // nothing queued survives the session
	tick := time.NewTicker(s.timing.Tick)
	defer tick.Stop()
	s.own.lastProbe = time.Now()
	for {
		var err error
		select {
		case <-ctx.Done():
			return nil
		case <-s.done:
			return nil
		case err = <-s.c.errs:
			return fmt.Errorf("wing: %w: %w", driver.ErrConnectionLost, err)
		case m := <-s.c.inbound:
			s.onMessage(m)
		case <-s.cmdReady:
			err = s.flushPending()
		case <-s.meterReady:
			err = s.requestMeters(time.Now())
		case <-s.rereadReady:
			err = s.requestReread(time.Now())
		case now := <-tick.C:
			err = s.onTick(now)
		}
		if err != nil {
			return err
		}
		if !s.flushOutbox(ctx, events) {
			return nil
		}
	}
}

// emit queues an event for Run to deliver after the current step.
func (s *session) emit(ev driver.Event) { s.own.outbox = append(s.own.outbox, ev) }

// flushOutbox delivers queued events; false when ctx ended meanwhile.
func (s *session) flushOutbox(ctx context.Context, events chan<- driver.Event) bool {
	for len(s.own.outbox) > 0 {
		select {
		case events <- s.own.outbox[0]:
			s.own.outbox = s.own.outbox[1:]
		case <-ctx.Done():
			return false
		}
	}
	s.own.outbox = s.own.outbox[:0]
	return true
}

// lost wraps a send failure: the TCP connection is gone.
func lost(err error) error { return fmt.Errorf("wing: %w: %w", driver.ErrConnectionLost, err) }
