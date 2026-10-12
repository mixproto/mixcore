// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// maxEchoHistory is how many recent writes per parameter are matched against
// pushes, so the echo of an earlier write in a fast drag is still recognised.
// An unrecognised echo would look like a desk change and end the user's
// gesture, so this covers about 300 ms of a 100 Hz drag.
const maxEchoHistory = 32

// errAlreadyRunning is returned by a second concurrent Run.
var errAlreadyRunning = errors.New("session already running")

type pendingWrite struct {
	param  param
	arg    osc.Arg
	origin driver.Origin
}

type sentWrite struct {
	arg      osc.Arg
	origin   driver.Origin
	at       time.Time
	changes  bool // the value differed from the newest one seen
	evidence bool // its missing echo counts as client-limit evidence
}

// session is one live connection. Apply, SetMeters and Close may be called from
// any goroutine; everything else runs on the owner goroutine (Connect, then Run).
type session struct {
	name   string // family, for logs and errors
	prof   *Profile
	timing Timing
	params *paramTable
	c      *conn
	// ownLink is set when the session was made by Driver.Connect: closing the
	// session then closes its sockets. Sessions of a supervisor's link leave
	// the sockets open for the next session (X32-F28).
	ownLink *link
	log     *slog.Logger
	audit   func(driver.WriteRecord)

	ident    driver.Identity
	snapshot model.State
	caps     model.Capabilities
	absent   map[feature]bool // set during connect, read-only afterwards
	inputs   int              // input strips of this console (XA-F72), set during connect
	headamps int              // unit headamps that answered, set during connect
	extra    bool             // the extra headamp groups answered, set during connect
	present  headampSet       // headamps read and reported (units, extras, patched in use)
	// knownUnits are the optional unit counts an earlier session of the same
	// link read (nil: first connect); those units are required (XA-F72).
	knownUnits *unitCounts

	mu      sync.Mutex
	order   []int                // pending parameter indexes, first-seen order
	rrSkip  map[int]bool         // parameters to leave out of the next re-read pass (held)
	pending map[int]pendingWrite // latest value per parameter (coalesced)

	done        chan struct{} // closed by Close
	cmdReady    chan struct{}
	meterReady  chan struct{}
	rereadReady chan struct{}
	metersOn    atomic.Bool
	stopped     atomic.Bool
	running     atomic.Bool
	closeOnce   sync.Once

	// Owner-goroutine state.
	// lastPong is when the last /xinfo reply arrived. Only replies prove that
	// our packets reach the console; pushes and meters keep flowing for up to
	// 10 s after our uplink dies. time.Now keeps a monotonic reading, so a
	// wall-clock step cannot fake or hide a loss.
	lastPong time.Time
	live     liveness
	lim      limitState
	scene    sceneState
	outbox   []driver.Event // events produced outside handleInbound, sent by Run
	tx       []byte
	msg      osc.Message
	sent     map[int][]sentWrite
	known    []model.Value // newest value seen per parameter (initial read, then every change)
	rr       rereadPass
}

var _ driver.Session = (*session)(nil)

func newSession(d *Driver, c *conn, opts driver.Options) *session {
	return &session{
		name: string(d.prof.Family), prof: &d.prof,
		timing: d.timing, params: &d.params, c: c, log: logger(opts), audit: opts.WriteAudit,
		pending: map[int]pendingWrite{}, done: make(chan struct{}), cmdReady: make(chan struct{}, 1), meterReady: make(chan struct{}, 1),
		rereadReady: make(chan struct{}, 1), tx: make([]byte, 0, rxBufSize), sent: map[int][]sentWrite{},
		rr: rereadPass{asked: map[int]bool{}}, scene: newSceneState(), present: newHeadampSet(d.params.layout.maxHeadamp()),
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
	p, ok := s.params.byRef(cmd.Param)
	if !ok || p.readOnly || s.isAbsent(p) {
		return fmt.Errorf("%s %s: %w", s.name, cmd.Param, driver.ErrUnsupported)
	}
	arg, err := toWire(p, cmd.Value)
	if err != nil {
		return err
	}
	idx := s.params.index(cmd.Param)
	s.mu.Lock()
	if _, queued := s.pending[idx]; !queued {
		s.order = append(s.order, idx)
	}
	s.pending[idx] = pendingWrite{param: p, arg: arg, origin: cmd.Origin}
	s.mu.Unlock()
	if s.stopped.Load() { // Run ended meanwhile: never send it, and say so
		s.dropPending()
		return driver.ErrClosed
	}
	signal(s.cmdReady)
	return nil
}

// SetMeters turns the input meter stream on or off.
func (s *session) SetMeters(req driver.MeterRequest) error {
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	s.metersOn.Store(req.Inputs)
	s.c.meters.SwitchOn(req.Inputs)
	signal(s.meterReady)
	return nil
}

// Close stops the session; queued writes are dropped, never sent. Meters stop
// being published at once. The sockets stay open unless the session owns them.
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		s.stopped.Store(true)
		s.c.meters.SwitchOn(false)
		close(s.done)
		s.dropPending()
		if l := s.ownLink; l != nil {
			l.release(s) // Close of the link must not close s again
			_ = l.Close()
		}
	})
	return nil
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default: // already signalled
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
	s.live = newLiveness(time.Now())
	tick := time.NewTicker(s.timing.LivenessTick)
	defer tick.Stop()
	renew := time.NewTicker(s.timing.RenewEvery)
	defer renew.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.done:
			return nil
		case <-s.c.done:
			return nil
		case err := <-s.c.errs:
			return fmt.Errorf("%s: %w: %w", s.name, driver.ErrConnectionLost, err)
		case p := <-s.c.inbound:
			if !s.handleInbound(ctx, p, events) {
				return nil
			}
		case <-s.cmdReady:
			s.flushPending()
		case <-s.meterReady:
			s.requestMeters()
		case <-s.rereadReady:
			s.requestReread(time.Now())
		case now := <-tick.C:
			if err := s.onTick(now); err != nil {
				return err
			}
		case <-renew.C:
			s.sendSub(addrXremote) // X32-F20, X32-F27
			s.requestMeters()
		}
		if !s.flushOutbox(ctx, events) {
			return nil
		}
	}
}

// emit queues an event for Run to deliver after the current step.
func (s *session) emit(ev driver.Event) { s.outbox = append(s.outbox, ev) }

// flushOutbox delivers queued events; false when ctx ended meanwhile.
func (s *session) flushOutbox(ctx context.Context, events chan<- driver.Event) bool {
	for len(s.outbox) > 0 {
		select {
		case events <- s.outbox[0]:
			s.outbox = s.outbox[1:]
		case <-ctx.Done():
			return false
		}
	}
	s.outbox = s.outbox[:0]
	return true
}

// sendSub sends an argument-less message on the subscription socket.
func (s *session) sendSub(addr string, args ...osc.Arg) {
	pkt, err := osc.AppendString(s.tx[:0], addr, args...)
	if err != nil {
		s.log.Error(s.name+": encode", "addr", addr, "err", err)
		return
	}
	s.tx = pkt
	if err := s.c.sendSub(pkt); err != nil {
		s.log.Debug(s.name+": send failed", "addr", addr, "err", err)
	}
}

// requestMeters (re)starts the family's meter streams while meters are on
// (x32: banks 1 and 2, X32-F50, X32-F52, X32-F55; xair: bank 1, XA-F57).
func (s *session) requestMeters() {
	if s.metersOn.Load() {
		for _, args := range s.prof.Meters.Requests() {
			s.sendSub(addrMeters, args...)
		}
	}
}

// flushPending sends every queued write from the command socket (X32-F23,
// XA section 2 "Echo design").
func (s *session) flushPending() {
	for _, w := range s.takePending() {
		pkt, err := osc.AppendString(s.tx[:0], w.param.addr, w.arg)
		if err != nil {
			s.log.Error(s.name+": encode write", "addr", w.param.addr, "err", err)
			continue
		}
		s.tx = pkt
		now := time.Now()
		if err := s.c.sendCmd(pkt); err != nil {
			s.log.Debug(s.name+": write failed", "addr", w.param.addr, "err", err)
			continue
		}
		idx := s.params.index(w.param.ref)
		s.rememberSent(idx, sentWrite{arg: w.arg, origin: w.origin, at: now, changes: s.changesKnown(idx, w),
			evidence: w.param.feature == featureCore})
		s.markTouched(idx)
		if s.audit != nil {
			s.audit(driver.WriteRecord{Param: w.param.ref, Address: w.param.addr, Origin: w.origin, At: now})
		}
	}
}

// changesKnown reports whether w sets a value other than the newest one we
// wrote (still in the echo history) or, without one, the newest one seen.
func (s *session) changesKnown(idx int, w pendingWrite) bool {
	if h := s.sent[idx]; len(h) > 0 {
		return !sameWire(w.param, h[len(h)-1].arg, w.arg)
	}
	v, err := fromWire(w.param, w.arg)
	return err != nil || idx >= len(s.known) || !s.known[idx].Equal(v)
}

func (s *session) rememberSent(idx int, w sentWrite) {
	h := append(s.sent[idx], w)
	if len(h) > maxEchoHistory {
		h = h[len(h)-maxEchoHistory:]
	}
	s.sent[idx] = h
}

// handleInbound turns one received packet into an event. It returns false
// when ctx was cancelled while the event was being delivered.
func (s *session) handleInbound(ctx context.Context, p inPacket, events chan<- driver.Event) bool {
	ev, ok := s.decodeChange(p)
	if !ok {
		return true
	}
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *session) decodeChange(p inPacket) (driver.ParamChanged, bool) {
	if err := osc.Decode(p.data, &s.msg); err != nil {
		s.log.Debug(s.name+": undecodable packet", "err", err)
		return driver.ParamChanged{}, false
	}
	if s.msg.AddressIs(addrXinfo) {
		s.lastPong = p.at // X32-F25
		return driver.ParamChanged{}, false
	}
	if !p.viaCmd && s.onMarker(p.at) { // scene markers, if the console pushes them (X32-F61, X32-F62, XA-F62)
		return driver.ParamChanged{}, false
	}
	par, ok := s.params.byAddress(s.msg.Address)
	if !ok || s.isAbsent(par) {
		return driver.ParamChanged{}, false // anything we did not ask for
	}
	arg, ok := s.msg.Arg(0)
	if !ok {
		return driver.ParamChanged{}, false
	}
	v, err := fromWire(par, arg)
	if err != nil {
		s.log.Debug(s.name+": value outside the documented encoding", "addr", par.addr, "err", err)
		return driver.ParamChanged{}, false
	}
	idx := s.params.index(par.ref)
	if par.conv == convHaIndex {
		s.onHeadampIndex(v) // a patch change: read the headamp it now uses (X32-F74)
	}
	if p.viaCmd {
		return s.rereadReply(idx, v, p.at)
	}
	s.markTouched(idx)
	s.remember(idx, v)
	s.onPush() // a push (or our echo) on the /xremote socket: we are served
	origin := s.matchEcho(idx, arg, p.at)
	if origin.Kind == driver.OriginConsole {
		s.onConsolePush(idx, p.at) // X32-F60: a recall is a burst of pushes
	}
	return driver.ParamChanged{Param: par.ref, Value: v, Origin: origin, At: p.at}, true
}

// matchEcho returns the origin of our own recent write with the same value,
// or a console origin. A matched write and everything older are forgotten.
func (s *session) matchEcho(idx int, arg osc.Arg, at time.Time) driver.Origin {
	h := s.sent[idx]
	for i := len(h) - 1; i >= 0; i-- {
		if at.Sub(h[i].at) > s.timing.EchoWindow || !sameWire(s.params.all[idx], h[i].arg, arg) {
			continue
		}
		origin := h[i].origin
		if rest := h[i+1:]; len(rest) > 0 {
			s.sent[idx] = rest
		} else {
			delete(s.sent, idx)
		}
		return origin
	}
	return driver.Origin{Kind: driver.OriginConsole}
}

// sameWire compares wire values of p; floats match within half a step of
// p's own law: the fader's 1023 steps (X32-F41) or the headamp's 144 or 64
// (X32-F72, xair.md XA-F116), never one tolerance for every float.
func sameWire(p param, a, b osc.Arg) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Type == osc.TypeFloat {
		d, tol := a.Float-b.Float, p.halfStep()
		return d <= tol && d >= -tol
	}
	return a.Equal(b)
}
