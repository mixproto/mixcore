// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
)

// SocketBinder binds every socket the engine opens to a chosen network before
// it is used. Android needs it: Go's sockets may bypass the app's network
// selection, so the app calls Network.bindSocket on each file descriptor (the
// Tailscale pattern). Apple and Windows never set one. BindSocket runs on an
// engine goroutine while the socket is being created and must return quickly.
type SocketBinder interface {
	BindSocket(fd int) error
}

type binderBox struct{ b SocketBinder }

var errBindPanic = errors.New("api: socket binder panicked")

// SetSocketBinder installs (or, with nil, removes) the binder. It applies to
// sockets opened afterwards: set it before Start and Discover.
func (e *Engine) SetSocketBinder(b SocketBinder) {
	if b == nil {
		e.binder.Store(nil)
		return
	}
	e.binder.Store(&binderBox{b: b})
}

// listenPacket is the engine's driver.ListenPacketFunc: OS sockets, passed
// through the socket binder when one is set.
func (e *Engine) listenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	var lc net.ListenConfig
	if box := e.binder.Load(); box != nil {
		lc.Control = func(_, _ string, c syscall.RawConn) error { return bindRaw(box.b, c) }
	}
	return lc.ListenPacket(ctx, network, address)
}

// dial is the engine's driver.DialFunc: OS TCP sockets, passed through the
// socket binder when one is set (Android binds them to the Wi-Fi network).
func (e *Engine) dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	if box := e.binder.Load(); box != nil {
		d.Control = func(_, _ string, c syscall.RawConn) error { return bindRaw(box.b, c) }
	}
	return d.DialContext(ctx, network, address)
}

func bindRaw(b SocketBinder, c syscall.RawConn) error {
	var bindErr error
	err := c.Control(func(fd uintptr) {
		defer func() {
			if r := recover(); r != nil {
				bindErr = errBindPanic
			}
		}()
		bindErr = b.BindSocket(int(fd))
	})
	if err != nil {
		return fmt.Errorf("api: socket control: %w", err)
	}
	if bindErr != nil {
		return fmt.Errorf("api: bind socket: %w", bindErr)
	}
	return nil
}
