// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
)

// Errors of Connect.
var (
	// ErrBadEndpoint is returned for an endpoint that is not an IP address.
	ErrBadEndpoint = errors.New("wing: endpoint must be an IP address and a port")
	// ErrNoReply: the console did not answer the schema walk or the initial
	// read in time.
	ErrNoReply = errors.New("wing: console did not answer")
)

// ErrProtocolMismatch wraps driver.ErrProtocolMismatch for WING: a schema
// that lacks or contradicts the modelled nodes, or a session that died of
// undefined bytes soon after connect. The link holds dialing off for
// mismatchFirstHold, doubling up to mismatchMaxHold, and retries forever
// (security re-check 2, go-reviewer L1). A definition flood (errWalkLimit)
// is not a mismatch.
var ErrProtocolMismatch = fmt.Errorf("wing: %w", driver.ErrProtocolMismatch)

// Mismatch backoff (design values).
const (
	mismatchFirstHold = time.Second
	mismatchMaxHold   = 30 * time.Second
	// streamGrace: a session that dies of undefined bytes within this long
	// after connect counts as a mismatch; one that ran longer does not.
	streamGrace = 5 * time.Second
)

// link keeps the meter socket for a whole engine session (its port is in
// every meter request, wing.md 2.4). It keeps no schema: every Connect
// walks the console again (security re-check 1: nothing one session read
// is trusted by the next). Each Connect
// dials a new TCP connection after closing the previous one with a FIN, so
// a lost session never holds one of the console's 16 (24) slots (WG-F22).
type link struct {
	drv    *Driver
	addr   string
	opts   driver.Options
	meters *meterSink

	mu     sync.Mutex
	cur    *session
	closed bool
	// mismatches counts protocol mismatches since the last healthy
	// session; dialing is held off until holdUntil (lastMismatch says why).
	mismatches   int
	holdUntil    time.Time
	lastMismatch error
}

var _ driver.Link = (*link)(nil)

// OpenLink implements driver.LinkOpener: it opens the meter socket. Nothing
// is sent until Connect.
func (d *Driver) OpenLink(ctx context.Context, ep driver.Endpoint, opts driver.Options) (_ driver.Link, err error) {
	defer safe.RecoverAsError(&err)
	return d.openLink(ctx, ep, opts)
}

func (d *Driver) openLink(ctx context.Context, ep driver.Endpoint, opts driver.Options) (*link, error) {
	console, err := resolve(ep)
	if err != nil {
		return nil, fmt.Errorf("%w: %q port %d", err, ep.Host, ep.Port)
	}
	m, err := openMeterSink(ctx, listenFunc(opts), console.Addr(), opts.Meters)
	if err != nil {
		return nil, err
	}
	return &link{drv: d, addr: console.String(), opts: opts, meters: m}, nil
}

// Connect implements driver.Link.
func (l *link) Connect(ctx context.Context) (_ driver.Session, err error) {
	defer safe.RecoverAsError(&err)
	if err := l.begin(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = l.failed(err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, l.drv.timing.ConnectTimeout)
	defer cancel()
	c, err := dialConn(ctx, dialFunc(l.opts), l.addr, l.drv.timing.WriteTimeout)
	if err != nil {
		return nil, err
	}
	s := newSession(l, c)
	defer func() {
		if err != nil {
			_ = s.Close() // also after a recovered panic
		}
	}()
	if err := s.connect(ctx); err != nil {
		return nil, fmt.Errorf("wing: connect %s: %w", l.addr, err)
	}
	if err := l.adopt(s); err != nil {
		return nil, err
	}
	return s, nil
}

// begin closes the previous session.
func (l *link) begin() error {
	l.mu.Lock()
	prev := l.cur
	l.cur = nil
	l.mu.Unlock()
	if prev != nil {
		_ = prev.Close()
	}
	if err := l.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return driver.ErrClosed
	}
	if wait := time.Until(l.holdUntil); wait > 0 {
		return fmt.Errorf("%w (next attempt in %s): %w", ErrProtocolMismatch, wait.Round(time.Millisecond), l.lastMismatch)
	}
	return nil
}

// adopt makes s current.
func (l *link) adopt(s *session) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return driver.ErrClosed
	}
	l.cur = s
	return nil
}

// failed classifies a failed connect: a schema error (not a definition
// flood) is a protocol mismatch, reported as one and held off. It returns
// the error to report.
func (l *link) failed(err error) error {
	if errors.Is(err, errWalkLimit) || !errors.Is(err, errSchema) {
		return err
	}
	l.noteMismatch(err)
	return fmt.Errorf("%w: %w", ErrProtocolMismatch, err)
}

// noteMismatch holds dialing off for 1 s, 2 s, 4 s ... up to 30 s.
func (l *link) noteMismatch(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hold := mismatchMaxHold
	if l.mismatches < 5 {
		hold = min(mismatchFirstHold<<l.mismatches, mismatchMaxHold)
	}
	l.mismatches++
	l.holdUntil, l.lastMismatch = time.Now().Add(hold), err
}

// healthy resets the mismatch count: a session ran for streamGrace.
func (l *link) healthy() {
	l.mu.Lock()
	l.mismatches = 0
	l.mu.Unlock()
}

// release forgets s as the current session (s is closing itself).
func (l *link) release(s *session) {
	l.mu.Lock()
	if l.cur == s {
		l.cur = nil
	}
	l.mu.Unlock()
}

// Err implements driver.Link: the meter socket failed.
func (l *link) Err() error {
	if err := l.meters.failure(); err != nil {
		return fmt.Errorf("wing: link broken: %w", err)
	}
	return nil
}

// Close implements driver.Link.
func (l *link) Close() error {
	l.mu.Lock()
	cur := l.cur
	l.cur, l.closed = nil, true
	l.mu.Unlock()
	if cur != nil {
		_ = cur.Close()
	}
	l.meters.close()
	return nil
}
