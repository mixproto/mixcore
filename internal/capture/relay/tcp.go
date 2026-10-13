// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mixproto/mixcore/internal/safe"
)

// TCP relays stream connections between apps and the console.
type TCP struct {
	Listener net.Listener
	Console  netip.AddrPort
	// Dial connects to the console for one app connection.
	Dial    func(ctx context.Context) (net.Conn, error)
	Rec     Recorder
	Apps    []netip.Addr // allow-list of app addresses; empty allows every source
	MaxApps int
	Logger  *slog.Logger
}

// Accept retry delays after a transient error (EMFILE, ENFILE, ECONNABORTED).
const (
	acceptRetryMin = 5 * time.Millisecond
	acceptRetryMax = time.Second
)

// Serve accepts until ctx ends or the listener is closed. It always closes
// the Listener and every connection it holds on return. A transient accept
// error (out of file descriptors, an aborted connection) is retried with a
// growing delay instead of ending the relay. The return value wraps
// ErrClosed after cancellation.
func (t *TCP) Serve(ctx context.Context) error {
	t.MaxApps = cmpOr(t.MaxApps, DefaultMaxApps)
	t.Logger = discard(t.Logger)
	t.Console = unmapped(t.Console)
	ctx, cancel := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(nil); _ = t.Listener.Close(); wg.Wait() }()
	stop := context.AfterFunc(ctx, func() { _ = t.Listener.Close() })
	defer stop()
	slots := make(chan struct{}, t.MaxApps)
	delay := time.Duration(0)
	for {
		conn, err := t.Listener.Accept()
		if ctx.Err() != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return fmt.Errorf("%w: %w", ErrClosed, context.Cause(ctx))
		}
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("relay: tcp accept: %w", err)
			}
			delay = min(max(2*delay, acceptRetryMin), acceptRetryMax)
			t.Logger.Warn("relay: accept failed, retrying", "err", err, "in", delay)
			if !sleepCtx(ctx, delay) {
				return fmt.Errorf("%w: %w", ErrClosed, context.Cause(ctx))
			}
			continue
		}
		delay = 0
		t.accepted(ctx, &wg, slots, conn, cancel)
	}
}

// accepted applies the allow-list and the connection limit, then serves conn.
func (t *TCP) accepted(ctx context.Context, wg *sync.WaitGroup, slots chan struct{}, conn net.Conn,
	cancel context.CancelCauseFunc) {
	if from := addrOf(conn.RemoteAddr()); !allowed(t.Apps, from.Addr()) {
		t.Logger.Debug("relay: source not in the allow-list refused", "from", from)
		_ = conn.Close()
		return
	}
	select {
	case slots <- struct{}{}:
	default:
		t.Logger.Warn("relay: too many app connections", "max", t.MaxApps)
		_ = conn.Close()
		return
	}
	safe.Go(wg, func(err error) { cancel(err) }, func() {
		defer func() { <-slots }()
		t.serveConn(ctx, conn)
	})
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}

func (t *TCP) serveConn(ctx context.Context, app net.Conn) {
	defer func() { _ = app.Close() }()
	appAddr := addrOf(app.RemoteAddr())
	up, err := t.Dial(ctx)
	if err != nil {
		t.Logger.Warn("relay: console unreachable", "console", t.Console, "err", err)
		return
	}
	defer func() { _ = up.Close() }()
	t.Logger.Info("relay: new app connection", "app", appAddr, "console", t.Console)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = app.Close(); _ = up.Close() })
	defer stop()
	var wg sync.WaitGroup
	wg.Go(func() { defer cancel(); t.pipe(up, app, appAddr, t.Console) })
	t.pipe(app, up, t.Console, appAddr)
	cancel()
	wg.Wait()
}

// pipe copies src to dst, recording each chunk as from -> to before it is
// written (see the package doc on order).
func (t *TCP) pipe(dst io.Writer, src io.Reader, from, to netip.AddrPort) {
	buf := make([]byte, readBuffer)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			// Record first: the peer cannot answer before it has the bytes, so
			// the capture keeps causal order (a request before its reply).
			if rerr := t.Rec.Stream(time.Now(), from, to, buf[:n]); rerr != nil {
				t.Logger.Error("relay: record", "err", rerr)
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				t.Logger.Debug("relay: stream ended", "from", from, "err", err)
			}
			return
		}
	}
}

func addrOf(a net.Addr) netip.AddrPort {
	if ta, ok := a.(*net.TCPAddr); ok {
		return unmapped(ta.AddrPort())
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.AddrPort{}
	}
	return unmapped(ap)
}
