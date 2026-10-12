// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package console supervises one console connection: connect, read, live; on
// loss keep the last values marked stale, reconnect with backoff and jitter,
// re-read, then unlock input. It is the single place that enforces the write
// gate (only user gestures and user macros write, never while stale) and the
// held-fader rule: an external change (desk, scene recall, another client) of
// a fader a person is holding is published at once and ends that gesture;
// losing the console cancels every hold. It never queues writes: a write that
// cannot be sent now is rejected, so nothing can be replayed after a reconnect.
// While live it asks the session for a slow, read-only background re-read
// (DefaultRereadEvery), skipped while any fader is held.
package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/backoff"
	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

const (
	// eventBuffer bounds driver events waiting for the supervisor.
	eventBuffer = 256
	// maxPendingChanges bounds changes kept between two Drain calls; beyond it
	// the next Drain returns a full snapshot instead.
	maxPendingChanges = 1024
	// stableAfter: a session that lived shorter than this counts as a failed
	// attempt, so a console that accepts and drops us at once still gets backoff
	// (and does not burn a push-client slot per retry). Design target.
	stableAfter = 10 * time.Second
)

// ErrAlreadyRunning is returned by Start on a running supervisor.
var ErrAlreadyRunning = errors.New("console: supervisor already running")

// Status is the connection state shown to the user.
type Status uint8

// Statuses.
const (
	StatusStopped    Status = iota // not started, stopped or paused
	StatusConnecting               // never connected yet in this run
	StatusLive                     // state is the console's; input unlocked
	StatusStale                    // connection lost: last values kept, input locked
)

func (s Status) String() string {
	return [...]string{"stopped", "connecting", "live", "stale", "unknown"}[min(int(s), 4)]
}

// Change is one parameter change applied to the state.
type Change struct {
	Param  model.ParamRef
	Value  model.Value
	Origin driver.Origin
}

// Update is what Drain returns: the current state plus what changed since
// the previous Drain. Full means the state was replaced (connect, reconnect,
// or too many changes to list). Events are never folded into Full.
type Update struct {
	Seq      uint64
	Status   Status
	Reason   string // why the status last changed (empty when live)
	Identity driver.Identity
	State    model.State
	Full     bool
	Changes  []Change
	Events   []HoldEvent
}

// Config configures a Supervisor.
type Config struct {
	Driver   driver.Driver
	Endpoint driver.Endpoint
	Options  driver.Options
	Policy   backoff.Policy      // zero: backoff.Default
	Meters   driver.MeterRequest // re-applied after every connect
	Logger   *slog.Logger
	Rand     func() float64 // jitter source in [0, 1); nil: math/rand/v2
	// RereadEvery is the background re-read interval while live (sessions
	// that implement driver.Rereader). Zero: DefaultRereadEvery; negative: off.
	RereadEvery time.Duration
}

// Supervisor owns the connection lifecycle of one console.
type Supervisor struct {
	cfg    Config
	notify chan struct{}
	life   sync.Mutex // serialises Start and Stop, including Stop's wait

	mu      sync.Mutex
	status  Status
	reason  string
	ident   driver.Identity
	state   model.State
	sess    driver.Session
	holds   holds
	changes []Change
	full    bool
	seq     uint64
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// link is the long-lived transport of drivers that implement
	// driver.LinkOpener. Only the run goroutine uses it; Stop closes it after
	// joining that goroutine, Pause keeps it for the next Start.
	link driver.Link
}

// New validates cfg and returns a stopped supervisor.
func New(cfg Config) (*Supervisor, error) {
	if cfg.Driver == nil {
		return nil, errors.New("console: Config.Driver is nil")
	}
	if cfg.Policy == (backoff.Policy{}) {
		cfg.Policy = backoff.Default
	}
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	if cfg.RereadEvery == 0 {
		cfg.RereadEvery = DefaultRereadEvery
	}
	return &Supervisor{
		cfg: cfg, notify: make(chan struct{}, 1),
		holds: newHolds(),
	}, nil
}

// Start launches the connect loop. It returns at once; progress is reported
// through Notify/Drain.
func (s *Supervisor) Start(ctx context.Context) error {
	s.life.Lock()
	defer s.life.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return ErrAlreadyRunning
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.setStatusLocked(StatusConnecting, "")
	onPanic := func(err error) { s.cfg.Logger.Error("console: supervisor panic", "err", err) }
	safe.Go(&s.wg, onPanic, func() { s.run(ctx) })
	return nil
}

// Stop ends the connect loop, closes the session and the link. Last values stay.
func (s *Supervisor) Stop() { s.halt(true) }

// Pause ends the connect loop and closes the session like Stop, but keeps the
// link: a Start soon after reuses the same sockets, so the console does not see
// a new client (X32-F28). Last values stay; the status is stopped.
func (s *Supervisor) Pause() { s.halt(false) }

func (s *Supervisor) halt(closeLink bool) {
	s.life.Lock()
	defer s.life.Unlock()
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
		s.mu.Lock()
		s.holds.cancelAll(StatusStopped.String())
		s.setStatusLocked(StatusStopped, "stopped")
		s.mu.Unlock()
	}
	if closeLink && s.link != nil {
		_ = s.link.Close()
		s.link = nil
	}
}

// Notify is signalled (capacity 1) whenever Drain has something new.
func (s *Supervisor) Notify() <-chan struct{} { return s.notify }

// Drain returns the current state and the changes since the previous Drain.
func (s *Supervisor) Drain() Update {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.snapshotLocked()
	u.Full = s.full
	if !s.full {
		u.Changes = s.changes
	}
	u.Events = s.holds.events
	s.changes, s.full, s.holds.events = nil, false, nil
	return u
}

// Snapshot returns the full current state without consuming pending changes.
func (s *Supervisor) Snapshot() Update {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.snapshotLocked()
	u.Full = true
	return u
}

func (s *Supervisor) snapshotLocked() Update {
	s.seq++
	return Update{Seq: s.seq, Status: s.status, Reason: s.reason, Identity: s.ident, State: s.state}
}

func (s *Supervisor) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Supervisor) setStatusLocked(st Status, reason string) {
	s.status, s.reason = st, reason
	s.full = true
	s.signal()
}

// run is the connect loop: connect, run, mark stale, back off, repeat.
func (s *Supervisor) run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		lived, err := s.connectAndRun(ctx)
		s.markStale(ctx, err)
		if lived >= stableAfter {
			attempt = 0
			continue // a healthy session was lost: retry at once
		}
		attempt++
		if !sleepCtx(ctx, s.cfg.Policy.Delay(attempt, s.cfg.Rand())) {
			return
		}
	}
}

// connectAndRun runs one session. The session is always closed, and a panic
// anywhere in it becomes an error, so the loop marks stale and reconnects.
func (s *Supervisor) connectAndRun(ctx context.Context) (lived time.Duration, err error) {
	defer safe.RecoverAsError(&err)
	started := time.Now()
	sess, err := s.connect(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sess.Close() }()
	s.adopt(sess)
	live := time.Now()
	s.cfg.Logger.Info("console: live", "endpoint", s.cfg.Endpoint.Host, "connect_ms", live.Sub(started).Milliseconds())
	err = s.runSession(ctx, sess)
	return time.Since(live), err
}

// connect makes one session: over the long-lived link when the driver has
// one (opened on first use, reopened only after it broke), else directly.
func (s *Supervisor) connect(ctx context.Context) (driver.Session, error) {
	opener, ok := s.cfg.Driver.(driver.LinkOpener)
	if !ok {
		return s.cfg.Driver.Connect(ctx, s.cfg.Endpoint, s.cfg.Options)
	}
	if s.link != nil && s.link.Err() != nil {
		s.cfg.Logger.Info("console: reopening sockets", "endpoint", s.cfg.Endpoint.Host, "err", s.link.Err())
		_ = s.link.Close()
		s.link = nil
	}
	if s.link == nil {
		l, err := opener.OpenLink(ctx, s.cfg.Endpoint, s.cfg.Options)
		if err != nil {
			return nil, err
		}
		s.link = l
	}
	return s.link.Connect(ctx)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// adopt makes a freshly read session current: the console's snapshot replaces
// local state (rule 1). Holds were already cancelled when the old session was lost.
func (s *Supervisor) adopt(sess driver.Session) {
	s.mu.Lock()
	s.sess = sess
	s.ident = sess.Identity()
	s.state = sess.Snapshot()
	s.changes = nil
	s.setStatusLocked(StatusLive, "")
	// SetMeters never blocks; calling it under mu orders it with SetMeters below.
	if err := sess.SetMeters(s.cfg.Meters); err != nil {
		s.cfg.Logger.Debug("console: meters", "err", err)
	}
	s.mu.Unlock()
}

// markStale keeps the last values, locks input and forgets the session.
func (s *Supervisor) markStale(ctx context.Context, cause error) {
	if s.cfg.Options.Meters != nil {
		s.cfg.Options.Meters.Reset()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sess = nil
	if ctx.Err() != nil {
		return // stopping: Stop sets the final status and cancels holds
	}
	s.holds.cancelAll(StatusStale.String())
	reason := "connection lost"
	if cause != nil {
		reason = cause.Error()
	}
	if s.status == StatusConnecting {
		s.reason = reason
		s.signal()
		return
	}
	if s.status != StatusStale {
		s.cfg.Logger.Info("console: stale", "endpoint", s.cfg.Endpoint.Host, "reason", reason)
	}
	s.setStatusLocked(StatusStale, reason)
}

func (s *Supervisor) runSession(ctx context.Context, sess driver.Session) error {
	ctx, cancel := context.WithCancel(ctx)
	events := make(chan driver.Event, eventBuffer)
	done := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }() // cancel first, then wait, or Run could block forever
	safe.Go(&wg, func(err error) { done <- err }, func() { done <- sess.Run(ctx, events) })
	rr, rereadC, stopReread := s.rereadTicker(sess)
	defer stopReread()
	for {
		select {
		case ev := <-events:
			s.handleEvent(ev)
		case <-rereadC:
			s.reread(rr)
		case err := <-done:
			return err
		}
	}
}

func (s *Supervisor) handleEvent(ev driver.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e := ev.(type) {
	case driver.ParamChanged:
		s.applyChangeLocked(Change{Param: e.Param, Value: e.Value, Origin: e.Origin})
	case driver.StateReplaced:
		s.state = e.State
		s.endHeldOnReplaceLocked()
		s.full = true
		s.signal()
	case driver.ClientLimitHit:
		s.cfg.Logger.Info("console: client limit hit, no updates", "limit", e.Limit)
		s.reason = fmt.Sprintf("console client limit (%d) reached", e.Limit)
		s.full = true
		s.signal()
	}
}

// applyChangeLocked publishes a console-reported value at once. If it is an
// external change of a held fader, that gesture ends (HoldExternalChange).
func (s *Supervisor) applyChangeLocked(c Change) {
	next, err := s.state.With(c.Param, c.Value)
	if err != nil {
		s.cfg.Logger.Debug("console: change ignored", "param", c.Param.String(), "err", err)
		return
	}
	s.state = next
	s.holds.external(c)
	if len(s.changes) >= maxPendingChanges {
		s.full = true
	} else if !s.full {
		s.changes = append(s.changes, c)
	}
	s.signal()
}

// Apply sends one user write to the console. It never queues: while the
// state is not live it returns driver.ErrStale (rule 3). A write for a fader
// whose gesture was ended returns ErrGestureEnded until a new hold-begin.
func (s *Supervisor) Apply(cmd driver.Command) error {
	if !cmd.Origin.MayWrite() {
		return driver.ErrNotUserOrigin // rule 2
	}
	s.mu.Lock()
	sess, status := s.sess, s.status
	ended := s.holds.isEnded(cmd.Param, cmd.Origin.ClientID)
	s.mu.Unlock()
	if status != StatusLive || sess == nil {
		return driver.ErrStale
	}
	if ended {
		return ErrGestureEnded
	}
	err := sess.Apply(cmd)
	if errors.Is(err, driver.ErrClosed) {
		return driver.ErrStale // the session died between the check and the call
	}
	return err
}

// SetMeters changes the meter request now and for every future connection.
func (s *Supervisor) SetMeters(req driver.MeterRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Meters = req
	if s.sess == nil {
		return nil
	}
	return s.sess.SetMeters(req)
}

// MeterSlot returns the slot meters are published to (may be nil).
func (s *Supervisor) MeterSlot() *meters.Slot { return s.cfg.Options.Meters }
