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

// Serve accepts until ctx ends. It closes the Listener and every connection
// it holds on return. The return value wraps ErrClosed after cancellation.
func (t *TCP) Serve(ctx context.Context) error {
	t.MaxApps = cmpOr(t.MaxApps, DefaultMaxApps)
	t.Logger = discard(t.Logger)
	t.Console = unmapped(t.Console)
	ctx, cancel := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(nil); wg.Wait() }()
	stop := context.AfterFunc(ctx, func() { _ = t.Listener.Close() })
	defer stop()
	slots := make(chan struct{}, t.MaxApps)
	for {
		conn, err := t.Listener.Accept()
		if ctx.Err() != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return fmt.Errorf("%w: %w", ErrClosed, context.Cause(ctx))
		}
		if err != nil {
			return fmt.Errorf("relay: tcp accept: %w", err)
		}
		if from := addrOf(conn.RemoteAddr()); !allowed(t.Apps, from.Addr()) {
			t.Logger.Debug("relay: source not in the allow-list refused", "from", from)
			_ = conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			t.Logger.Warn("relay: too many app connections", "max", t.MaxApps)
			_ = conn.Close()
			continue
		}
		safe.Go(&wg, func(err error) { cancel(err) }, func() {
			defer func() { <-slots }()
			t.serveConn(ctx, conn)
		})
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

// pipe copies src to dst, recording each chunk as from -> to.
func (t *TCP) pipe(dst io.Writer, src io.Reader, from, to netip.AddrPort) {
	buf := make([]byte, readBuffer)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			if rerr := t.Rec.Stream(time.Now(), from, to, buf[:n]); rerr != nil {
				t.Logger.Error("relay: record", "err", rerr)
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
