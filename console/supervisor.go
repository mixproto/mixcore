// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package console supervises one console connection: connect, read, live; on
// loss keep the last values marked stale, reconnect with backoff and jitter,
// re-read, then unlock input. It is the single place that enforces the write
// gate (only user gestures and user macros write, never while stale) and the
// held-fader rule (console updates for a fader a person is holding are applied
// when it is released). It never queues writes: a write that cannot be sent
// now is rejected, so nothing can be replayed after a reconnect.
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
// or too many changes to list).
type Update struct {
	Seq      uint64
	Status   Status
	Reason   string // why the status last changed (empty when live)
	Identity driver.Identity
	State    model.State
	Full     bool
	Changes  []Change
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
}

// Supervisor owns the connection lifecycle of one console.
type Supervisor struct {
	cfg    Config
	notify chan struct{}

	mu       sync.Mutex
	status   Status
	reason   string
	ident    driver.Identity
	state    model.State
	sess     driver.Session
	held     map[model.ParamRef]map[string]struct{}
	deferred map[model.ParamRef]Change
	changes  []Change
	full     bool
	seq      uint64
	cancel   context.CancelFunc
	wg       sync.WaitGroup
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
	return &Supervisor{
		cfg: cfg, notify: make(chan struct{}, 1),
		held: map[model.ParamRef]map[string]struct{}{}, deferred: map[model.ParamRef]Change{},
	}, nil
}

// Start launches the connect loop. It returns at once; progress is reported
// through Notify/Drain.
func (s *Supervisor) Start(ctx context.Context) error {
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

// Stop ends the connect loop and closes the session. Last values stay.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	s.wg.Wait()
	s.mu.Lock()
	s.setStatusLocked(StatusStopped, "stopped")
	s.mu.Unlock()
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
	s.changes, s.full = nil, false
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
		started := time.Now()
		sess, err := s.cfg.Driver.Connect(ctx, s.cfg.Endpoint, s.cfg.Options)
		if err != nil {
			s.markStale(ctx, err)
			attempt++
			if !sleepCtx(ctx, s.cfg.Policy.Delay(attempt, s.cfg.Rand())) {
				return
			}
			continue
		}
		attempt = 0
		s.adopt(sess)
		s.cfg.Logger.Info("console: live", "endpoint", s.cfg.Endpoint.Host, "connect_ms", time.Since(started).Milliseconds())
		err = s.runSession(ctx, sess)
		_ = sess.Close()
		s.markStale(ctx, err)
	}
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
// local state (rule 1), values held aside for released faders are discarded.
func (s *Supervisor) adopt(sess driver.Session) {
	s.mu.Lock()
	s.sess = sess
	s.ident = sess.Identity()
	s.state = sess.Snapshot()
	s.deferred = map[model.ParamRef]Change{}
	s.changes = nil
	s.setStatusLocked(StatusLive, "")
	meterReq := s.cfg.Meters
	s.mu.Unlock()
	if err := sess.SetMeters(meterReq); err != nil {
		s.cfg.Logger.Debug("console: meters", "err", err)
	}
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
		return // stopping: Stop sets the final status
	}
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
	for {
		select {
		case ev := <-events:
			s.handleEvent(ev)
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
		s.deferred = map[model.ParamRef]Change{}
		s.full = true
		s.signal()
	case driver.ClientLimitHit:
		s.cfg.Logger.Info("console: client limit hit, no updates", "limit", e.Limit)
		s.reason = fmt.Sprintf("console client limit (%d) reached", e.Limit)
		s.full = true
		s.signal()
	}
}

// applyChangeLocked applies a console-reported value, or keeps it aside while
// a person holds that fader (touch wins while held).
func (s *Supervisor) applyChangeLocked(c Change) {
	if len(s.held[c.Param]) > 0 {
		s.deferred[c.Param] = c
		return
	}
	next, err := s.state.With(c.Param, c.Value)
	if err != nil {
		s.cfg.Logger.Debug("console: change ignored", "param", c.Param.String(), "err", err)
		return
	}
	s.state = next
	if len(s.changes) >= maxPendingChanges {
		s.full = true
	} else if !s.full {
		s.changes = append(s.changes, c)
	}
	s.signal()
}

// Apply sends one user write to the console. It never queues: while the
// state is not live it returns driver.ErrStale (rule 3).
func (s *Supervisor) Apply(cmd driver.Command) error {
	if !cmd.Origin.MayWrite() {
		return driver.ErrNotUserOrigin // rule 2
	}
	s.mu.Lock()
	sess, status := s.sess, s.status
	s.mu.Unlock()
	if status != StatusLive || sess == nil {
		return driver.ErrStale
	}
	err := sess.Apply(cmd)
	if errors.Is(err, driver.ErrClosed) {
		return driver.ErrStale // the session died between the check and the call
	}
	return err
}

// Hold marks a fader as held (true) or released (false) by origin's client.
// On the last release the console's latest value, kept aside while held, is
// applied. Holding never writes.
func (s *Supervisor) Hold(p model.ParamRef, origin driver.Origin, held bool) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if p.Field != model.FieldFader {
		return fmt.Errorf("console: hold %s: %w", p, driver.ErrUnsupported)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	holders := s.held[p]
	if held {
		if holders == nil {
			holders = map[string]struct{}{}
			s.held[p] = holders
		}
		holders[origin.ClientID] = struct{}{}
		return nil
	}
	delete(holders, origin.ClientID)
	if len(holders) > 0 {
		return nil
	}
	delete(s.held, p)
	if c, ok := s.deferred[p]; ok {
		delete(s.deferred, p)
		s.applyChangeLocked(c)
	}
	return nil
}

// SetMeters changes the meter request now and for every future connection.
func (s *Supervisor) SetMeters(req driver.MeterRequest) error {
	s.mu.Lock()
	s.cfg.Meters = req
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.SetMeters(req)
}

// MeterSlot returns the slot meters are published to (may be nil).
func (s *Supervisor) MeterSlot() *meters.Slot { return s.cfg.Options.Meters }
