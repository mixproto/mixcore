// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"fmt"
	"sync"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
)

// link keeps the two sockets of a console for a whole engine session, so
// reconnects never change our source ports: the console counts every new
// port as a new client and keeps its slot for 10 s (X32-F28).
type link struct {
	drv  *Driver
	c    *conn
	opts driver.Options

	mu     sync.Mutex
	cur    *session
	closed bool
}

var (
	_ driver.LinkOpener = (*Driver)(nil)
	_ driver.Link       = (*link)(nil)
)

// OpenLink implements driver.LinkOpener: it opens the subscription and the
// write socket once. Nothing is sent until Connect.
func (d *Driver) OpenLink(ctx context.Context, ep driver.Endpoint, opts driver.Options) (_ driver.Link, err error) {
	defer safe.RecoverAsError(&err)
	return d.openLink(ctx, ep, opts)
}

func (d *Driver) openLink(ctx context.Context, ep driver.Endpoint, opts driver.Options) (*link, error) {
	console, err := resolve(ep)
	if err != nil {
		return nil, err
	}
	c, err := openConn(ctx, console, opts)
	if err != nil {
		return nil, err
	}
	return &link{drv: d, c: c, opts: opts}, nil
}

// Connect implements driver.Link. The previous session, if still open, is
// closed first, and packets left over from it are discarded.
func (l *link) Connect(ctx context.Context) (_ driver.Session, err error) {
	defer safe.RecoverAsError(&err)
	s, err := l.begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = s.Close() // also after a recovered panic
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, l.drv.timing.ConnectTimeout)
	defer cancel()
	if err := s.connect(ctx); err != nil {
		return nil, fmt.Errorf("x32: connect %s: %w", l.c.console, err)
	}
	return s, nil
}

// begin closes the previous session and makes a new one current.
func (l *link) begin() (*session, error) {
	l.mu.Lock()
	prev := l.cur
	l.cur = nil
	closed := l.closed
	l.mu.Unlock()
	if prev != nil {
		_ = prev.Close()
	}
	if closed {
		return nil, driver.ErrClosed
	}
	if err := l.Err(); err != nil {
		return nil, err
	}
	l.c.discardInbound()
	s := newSession(l.drv, l.c, l.opts)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed { // Close raced with us
		return nil, driver.ErrClosed
	}
	l.cur = s
	return s, nil
}

// release forgets s as the current session (s is closing itself).
func (l *link) release(s *session) {
	l.mu.Lock()
	if l.cur == s {
		l.cur = nil
	}
	l.mu.Unlock()
}

// Err implements driver.Link.
func (l *link) Err() error {
	if err := l.c.failure(); err != nil {
		return fmt.Errorf("x32: link broken: %w", err)
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
	l.c.close()
	return nil
}
