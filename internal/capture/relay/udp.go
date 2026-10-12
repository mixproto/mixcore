// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/mixproto/mixcore/internal/safe"
)

// UDP relays datagrams between apps and the console.
type UDP struct {
	Listen  net.PacketConn // where apps send (the relay's address)
	Console netip.AddrPort
	// Upstream opens the socket that talks to the console for one app.
	Upstream func(ctx context.Context) (net.PacketConn, error)
	Rec      Recorder
	Apps     []netip.Addr  // allow-list of app addresses; empty allows every source
	Idle     time.Duration // an app silent this long loses its upstream socket
	MaxApps  int
	Logger   *slog.Logger
}

type udpApp struct {
	addr     netip.AddrPort
	up       net.PacketConn
	lastSeen time.Time
}

// Serve relays until ctx ends; it closes the upstream sockets it opened but
// not Listen. The return value wraps ErrClosed after cancellation.
func (u *UDP) Serve(ctx context.Context) error {
	u.Idle = cmpOr(u.Idle, DefaultUDPIdle)
	u.MaxApps = cmpOr(u.MaxApps, DefaultMaxApps)
	u.Logger = discard(u.Logger)
	u.Console = unmapped(u.Console)
	ctx, cancel := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	apps := map[netip.AddrPort]*udpApp{}
	var mu sync.Mutex
	defer func() {
		cancel(nil)
		mu.Lock()
		for _, a := range apps {
			_ = a.up.Close()
		}
		mu.Unlock()
		wg.Wait()
	}()
	stop := context.AfterFunc(ctx, func() { _ = u.Listen.SetReadDeadline(time.Now()) })
	defer stop()
	onPanic := func(err error) { cancel(err) }
	safe.Go(&wg, onPanic, func() { u.reap(ctx, &mu, apps) })
	err := u.readApps(ctx, &wg, &mu, apps, onPanic)
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", ErrClosed, context.Cause(ctx))
	}
	return err
}

func cmpOr[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

func (u *UDP) readApps(ctx context.Context, wg *sync.WaitGroup, mu *sync.Mutex, apps map[netip.AddrPort]*udpApp,
	onPanic func(error)) error {
	buf := make([]byte, readBuffer)
	for {
		n, from, err := u.Listen.ReadFrom(buf)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("relay: udp read: %w", err)
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		appAddr := unmapped(ua.AddrPort())
		if !allowed(u.Apps, appAddr.Addr()) {
			u.Logger.Debug("relay: source not in the allow-list dropped", "from", appAddr)
			continue
		}
		a, err := u.app(ctx, wg, mu, apps, appAddr, onPanic)
		if err != nil {
			u.Logger.Warn("relay: app not served", "app", appAddr, "err", err)
			continue
		}
		u.forward(a, buf[:n])
	}
}

// app returns the upstream state for addr, opening a socket for a new app.
func (u *UDP) app(ctx context.Context, wg *sync.WaitGroup, mu *sync.Mutex, apps map[netip.AddrPort]*udpApp,
	addr netip.AddrPort, onPanic func(error)) (*udpApp, error) {
	mu.Lock()
	defer mu.Unlock()
	if a := apps[addr]; a != nil {
		a.lastSeen = time.Now()
		return a, nil
	}
	if len(apps) >= u.MaxApps {
		return nil, fmt.Errorf("relay: more than %d apps", u.MaxApps)
	}
	up, err := u.Upstream(ctx)
	if err != nil {
		return nil, fmt.Errorf("relay: upstream socket: %w", err)
	}
	a := &udpApp{addr: addr, up: up, lastSeen: time.Now()}
	apps[addr] = a
	u.Logger.Info("relay: new app", "app", addr, "console", u.Console)
	safe.Go(wg, onPanic, func() { u.readConsole(ctx, a) })
	return a, nil
}

func (u *UDP) forward(a *udpApp, b []byte) {
	if _, err := a.up.WriteTo(b, net.UDPAddrFromAddrPort(u.Console)); err != nil {
		u.Logger.Debug("relay: to console", "err", err)
		return
	}
	if err := u.Rec.Datagram(time.Now(), a.addr, u.Console, b); err != nil {
		u.Logger.Error("relay: record", "err", err)
	}
}

// readConsole forwards what the console sends to this app's socket.
func (u *UDP) readConsole(ctx context.Context, a *udpApp) {
	buf := make([]byte, readBuffer)
	for {
		n, from, err := a.up.ReadFrom(buf)
		if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				u.Logger.Debug("relay: from console", "err", err)
			}
			return
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok || unmapped(ua.AddrPort()) != u.Console {
			continue // only the console may reach the app through us
		}
		if _, err := u.Listen.WriteTo(buf[:n], net.UDPAddrFromAddrPort(a.addr)); err != nil {
			u.Logger.Debug("relay: to app", "err", err)
			continue
		}
		if err := u.Rec.Datagram(time.Now(), u.Console, a.addr, buf[:n]); err != nil {
			u.Logger.Error("relay: record", "err", err)
		}
	}
}

// reap closes the upstream socket of apps silent for longer than Idle.
func (u *UDP) reap(ctx context.Context, mu *sync.Mutex, apps map[netip.AddrPort]*udpApp) {
	t := time.NewTicker(u.Idle / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			mu.Lock()
			for addr, a := range apps {
				if now.Sub(a.lastSeen) > u.Idle {
					_ = a.up.Close()
					delete(apps, addr)
					u.Logger.Info("relay: app idle, upstream closed", "app", addr)
				}
			}
			mu.Unlock()
		}
	}
}
