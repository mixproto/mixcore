// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

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
var errAlreadyRunning = errors.New("x32: session already running")

type pendingWrite struct {
	param  param
	arg    osc.Arg
	origin driver.Origin
}

type sentWrite struct {
	arg    osc.Arg
	origin driver.Origin
	at     time.Time
}

// session is one live connection. Apply, SetMeters and Close may be called from
// any goroutine; everything else runs on the owner goroutine (Connect, then Run).
type session struct {
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

	mu      sync.Mutex
	order   []int                // pending parameter indexes, first-seen order
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
	tx       []byte
	msg      osc.Message
	sent     map[int][]sentWrite
	known    []model.Value // newest value seen per parameter (initial read, then every change)
	rr       rereadPass
}

var _ driver.Session = (*session)(nil)

func newSession(d *Driver, c *conn, opts driver.Options) *session {
	return &session{
		timing: d.timing, params: &d.params, c: c, log: logger(opts), audit: opts.WriteAudit,
		pending: map[int]pendingWrite{}, done: make(chan struct{}), cmdReady: make(chan struct{}, 1), meterReady: make(chan struct{}, 1),
		rereadReady: make(chan struct{}, 1), tx: make([]byte, 0, rxBufSize), sent: map[int][]sentWrite{},
		rr: rereadPass{asked: map[int]bool{}},
	}
}

func (s *session) Identity() driver.Identity        { return s.ident }
func (s *session) Capabilities() model.Capabilities { return capabilities() }
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
	if !ok {
		return fmt.Errorf("x32 %s: %w", cmd.Param, driver.ErrUnsupported)
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
	s.c.meters.enabled.Store(req.Inputs)
	signal(s.meterReady)
	return nil
}

// Close stops the session; queued writes are dropped, never sent. Meters stop
// being published at once. The sockets stay open unless the session owns them.
func (s *session) Close() error {
	s.closeOnce.Do(func() {
		s.stopped.Store(true)
		s.c.meters.enabled.Store(false)
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
			return fmt.Errorf("x32: %w: %w", driver.ErrConnectionLost, err)
		case p := <-s.c.inbound:
			if !s.handleInbound(ctx, p, events) {
				return nil
			}
		case <-s.cmdReady:
			s.flushPending()
		case <-s.meterReady:
			s.requestMeters()
		case <-s.rereadReady:
			s.startReread(time.Now())
		case now := <-tick.C:
			if err := s.onTick(now); err != nil {
				return err
			}
		case <-renew.C:
			s.sendSub(addrXremote) // X32-F20, X32-F27
			s.requestMeters()
		}
	}
}

// sendSub sends an argument-less message on the subscription socket.
func (s *session) sendSub(addr string, args ...osc.Arg) {
	pkt, err := osc.AppendString(s.tx[:0], addr, args...)
	if err != nil {
		s.log.Error("x32: encode", "addr", addr, "err", err)
		return
	}
	s.tx = pkt
	if err := s.c.sendSub(pkt); err != nil {
		s.log.Debug("x32: send failed", "addr", addr, "err", err)
	}
}

// requestMeters (re)starts the bank 1 stream while meters are on (X32-F50, X32-F52).
func (s *session) requestMeters() {
	if s.metersOn.Load() {
		s.sendSub(addrMeters, osc.StringBytes([]byte(meterBank1)))
	}
}

// flushPending sends every queued write from the command socket (X32-F23).
func (s *session) flushPending() {
	for _, w := range s.takePending() {
		pkt, err := osc.AppendString(s.tx[:0], w.param.addr, w.arg)
		if err != nil {
			s.log.Error("x32: encode write", "addr", w.param.addr, "err", err)
			continue
		}
		s.tx = pkt
		now := time.Now()
		if err := s.c.sendCmd(pkt); err != nil {
			s.log.Debug("x32: write failed", "addr", w.param.addr, "err", err)
			continue
		}
		idx := s.params.index(w.param.ref)
		s.rememberSent(idx, sentWrite{arg: w.arg, origin: w.origin, at: now})
		s.markTouched(idx)
		if s.audit != nil {
			s.audit(driver.WriteRecord{Param: w.param.ref, Address: w.param.addr, Origin: w.origin, At: now})
		}
	}
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
		s.log.Debug("x32: undecodable packet", "err", err)
		return driver.ParamChanged{}, false
	}
	if s.msg.AddressIs(addrXinfo) {
		s.lastPong = p.at // X32-F25
		return driver.ParamChanged{}, false
	}
	par, ok := s.params.byAddress(s.msg.Address)
	if !ok {
		return driver.ParamChanged{}, false // anything we did not ask for
	}
	arg, ok := s.msg.Arg(0)
	if !ok {
		return driver.ParamChanged{}, false
	}
	v, err := fromWire(par, arg)
	if err != nil {
		s.log.Debug("x32: value outside the documented encoding", "addr", par.addr, "err", err)
		return driver.ParamChanged{}, false
	}
	idx := s.params.index(par.ref)
	if p.viaCmd {
		return s.rereadReply(idx, v, p.at)
	}
	s.markTouched(idx)
	s.remember(idx, v)
	origin := s.matchEcho(idx, arg, p.at)
	return driver.ParamChanged{Param: par.ref, Value: v, Origin: origin, At: p.at}, true
}

// matchEcho returns the origin of our own recent write with the same value,
// or a console origin. A matched write and everything older are forgotten.
func (s *session) matchEcho(idx int, arg osc.Arg, at time.Time) driver.Origin {
	h := s.sent[idx]
	for i := len(h) - 1; i >= 0; i-- {
		if at.Sub(h[i].at) > s.timing.EchoWindow || !sameWire(h[i].arg, arg) {
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

// sameWire compares wire values; floats match within half a fader step (X32-F41).
func sameWire(a, b osc.Arg) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Type == osc.TypeFloat {
		d := float64(a.Float - b.Float)
		return d <= 0.5/faderSteps && d >= -0.5/faderSteps
	}
	return a.Equal(b)
}
