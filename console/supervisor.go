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
	// OfflineAfter: a stale console is labelled offline after this long
	// (design 8.1, decision D5). A label only: retries keep their pace.
	OfflineAfter = 30 * time.Second
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
	Seq        uint64
	Status     Status
	Reason     string    // why the status last changed (empty when live)
	StaleSince time.Time // when the status became stale; zero unless stale
	Offline    bool      // stale for at least OfflineAfter
	// Limited: live, but the console does not send us updates (client slots
	// full). Input is locked like stale; LimitedReason says why we think so.
	Limited       bool
	LimitedReason string
	Identity      driver.Identity
	// Capabilities of the current (or last) session; zero before the first connect.
	Capabilities model.Capabilities
	State        model.State
	Full         bool
	Changes      []Change
	// Effective lists strips whose effective mute changed (diffs only; a full
	// update carries every strip's effective mute in State).
	Effective []EffectiveChange
	Events    []HoldEvent
	// Scenes are scene recalls detected at the console since the last Drain
	// (never folded into Full, like Events).
	Scenes []driver.SceneRecalled
	// ShowMode is on (SR-27); Unlocked lists the headamps whose Gain raise is
	// unlocked; UnlockEnded are unlocks that ended since the last Drain.
	ShowMode    bool
	Unlocked    []int
	UnlockEnded []UnlockEvent
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
	// Safety are the phone safety defaults (slip guard, monitor ceiling, show
	// mode); see safety.go.
	Safety SafetyConfig
	// RereadEvery is the background re-read interval while live (sessions
	// that implement driver.Rereader). Zero: DefaultRereadEvery; negative: off.
	RereadEvery time.Duration
}

// Supervisor owns the connection lifecycle of one console.
type Supervisor struct {
	cfg    Config
	notify chan struct{}
	life   sync.Mutex // serialises Start and Stop, including Stop's wait

	mu         sync.Mutex
	status     Status
	reason     string
	staleSince time.Time
	offline    bool
	limited    string // reason while the console does not send us updates
	offlineT   *time.Timer
	ident      driver.Identity
	caps       model.Capabilities
	state      model.State
	sess       driver.Session
	holds      holds
	safe       safety
	changes    []Change
	eff        effective
	scenes     []driver.SceneRecalled
	full       bool
	seq        uint64
	cancel     context.CancelFunc
	wg         sync.WaitGroup

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
		holds: newHolds(), safe: newSafety(cfg.Safety),
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
		s.safe.unlocks.endAll(UnlockStopped)
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
		u.Changes, u.Effective = s.changes, s.eff.changes
	}
	u.Events, u.Scenes, u.UnlockEnded = s.holds.events, s.scenes, s.safe.unlocks.events
	s.changes, s.full, s.holds.events, s.eff.changes, s.scenes = nil, false, nil, nil, nil
	s.safe.unlocks.events = nil
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
	return Update{Seq: s.seq, Status: s.status, Reason: s.reason, StaleSince: s.staleSince, Offline: s.offline,
		Limited: s.limited != "", LimitedReason: s.limited, Identity: s.ident, Capabilities: s.caps, State: s.state,
		ShowMode: s.safe.showMode, Unlocked: s.safe.unlocks.list()}
}

func (s *Supervisor) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Supervisor) setStatusLocked(st Status, reason string) {
	s.limited = "" // every status change starts a new session's evidence
	if st == StatusStale && s.status != StatusStale {
		s.staleSince = time.Now()
		s.offlineT = time.AfterFunc(OfflineAfter, s.markOffline)
	}
	if st != StatusStale {
		s.staleSince, s.offline = time.Time{}, false
		if s.offlineT != nil {
			s.offlineT.Stop()
			s.offlineT = nil
		}
	}
	s.status, s.reason = st, reason
	s.full = true
	s.signal()
}

// markOffline labels a console that has been stale for OfflineAfter.
func (s *Supervisor) markOffline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != StatusStale || s.offline || time.Since(s.staleSince) < OfflineAfter {
		return // went live (or stopped) meanwhile
	}
	s.offline = true
	s.cfg.Logger.Info("console: offline", "endpoint", s.cfg.Endpoint.Host)
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
	s.caps = sess.Capabilities()
	s.safe.adoptState(s.state, sess.Snapshot())
	s.state = sess.Snapshot()
	s.changes = nil
	s.eff.reset(s.state)
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
	s.safe.unlocks.endAll(UnlockStale)
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
		s.eff.reset(s.state)
		s.endHeldOnReplaceLocked()
		s.full = true
		s.signal()
	case driver.ClientLimitHit:
		s.cfg.Logger.Info("console: client limit hit, no updates", "limit", e.Limit, "reason", e.Reason)
		s.limited = e.Reason
		s.holds.cancelAll(ReasonLimited)
		s.safe.unlocks.endAll(UnlockStale) // limited is stale for input (SR-05)
		s.full = true
		s.signal()
	case driver.SceneRecalled:
		// SR-15: a recall at the desk ends every held fader, even one whose own
		// update has not arrived (or was lost); the driver re-reads meanwhile.
		s.endHeldOnReplaceLocked()
		if len(s.scenes) < maxPendingEvents {
			s.scenes = append(s.scenes, e)
		}
		s.signal()
	case driver.ClientLimitCleared:
		s.cfg.Logger.Info("console: updates arrive again")
		s.limited = ""
		s.full = true
		s.signal()
	}
}

// applyChangeLocked publishes a console-reported value at once. If it is an
// external change of a held fader, that gesture ends (HoldExternalChange).
func (s *Supervisor) applyChangeLocked(c Change) {
	_, known := s.state.Strip(c.Param.Strip)
	next, err := s.state.With(c.Param, c.Value)
	if err != nil {
		s.cfg.Logger.Debug("console: change ignored", "param", c.Param.String(), "err", err)
		return
	}
	s.state = next
	s.holds.external(c)
	s.safe.noteChange(c)
	if !known || len(s.changes)+len(s.eff.changes) >= maxPendingChanges { // a new headamp (x32 patch change) needs its snapshot entry
		s.full = true
	} else if !s.full {
		s.changes = append(s.changes, c)
	}
	if affectsMute(c.Param.Field) {
		s.eff.update(s.state, c.Origin, !s.full)
	}
	s.signal()
}

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
