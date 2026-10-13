// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/internal/wingproto"
)

// Errors of Connect.
var (
	// ErrBadEndpoint is returned for an endpoint that is not an IP address.
	ErrBadEndpoint = errors.New("wing: endpoint must be an IP address and a port")
	// ErrNoReply: the console did not answer the schema walk or the initial
	// read in time.
	ErrNoReply = errors.New("wing: console did not answer")
	// ErrProtocolMismatch: the console's schema failed maxSchemaFailures
	// connects in a row; the link stops dialing until the engine restarts
	// (review go L1), so a console that is not what mixcore expects is not
	// hammered and the user sees why.
	ErrProtocolMismatch = errors.New("wing: the console's schema does not match what mixcore needs; not retrying until the engine is restarted")
)

// maxSchemaFailures is how many schema failures in a row stop the link.
const maxSchemaFailures = 3

// schemaCache is the result of a schema walk, kept for the link: the same
// firmware has the same tree (WG-F32: hashes are stable), so a reconnect
// only reads values.
type schemaCache struct {
	firmware string
	defs     map[string]wingproto.Def
}

// link keeps the meter socket for a whole engine session (its port is in
// every meter request, wing.md 2.4) and the schema cache. Each Connect
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
	cache  *schemaCache
	seq    uint32
	// schemaFails counts schema failures in a row; mismatch is set once
	// they reach maxSchemaFailures.
	schemaFails int
	mismatch    error
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
	seq, cache, err := l.begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			l.failed(err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, l.drv.timing.ConnectTimeout)
	defer cancel()
	c, err := dialConn(ctx, dialFunc(l.opts), l.addr, l.drv.timing.WriteTimeout)
	if err != nil {
		return nil, err
	}
	s := newSession(l, c, seq)
	defer func() {
		if err != nil {
			_ = s.Close() // also after a recovered panic
		}
	}()
	walked, err := s.connect(ctx, cache)
	if err != nil {
		return nil, fmt.Errorf("wing: connect %s: %w", l.addr, err)
	}
	if err := l.adopt(s, walked); err != nil {
		return nil, err
	}
	return s, nil
}

// begin closes the previous session and returns this session's number and
// the schema cache.
func (l *link) begin() (uint32, *schemaCache, error) {
	l.mu.Lock()
	prev := l.cur
	l.cur = nil
	l.mu.Unlock()
	if prev != nil {
		_ = prev.Close()
	}
	if err := l.Err(); err != nil {
		return 0, nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, nil, driver.ErrClosed
	}
	if l.mismatch != nil {
		return 0, nil, l.mismatch
	}
	l.seq++
	return l.seq, l.cache, nil
}

// adopt makes s current and keeps its schema.
func (l *link) adopt(s *session, walked *schemaCache) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return driver.ErrClosed
	}
	if walked != nil {
		l.cache = walked
	}
	l.cur = s
	l.schemaFails = 0
	return nil
}

// failed drops the schema cache after any failed connect (review M2: a
// cache is only ever what a successful connect read) and counts schema
// failures.
func (l *link) failed(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if errors.Is(err, ErrProtocolMismatch) {
		return
	}
	l.cache = nil
	if !errors.Is(err, errSchema) {
		l.schemaFails = 0
		return
	}
	if l.schemaFails++; l.schemaFails >= maxSchemaFailures {
		l.mismatch = fmt.Errorf("%w: %w", ErrProtocolMismatch, err)
	}
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
