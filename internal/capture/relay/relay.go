// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package relay forwards a vendor app's traffic to the console and back,
// unchanged, and records both directions. The vendor app connects to the
// relay's address instead of the console's (only possible where the app
// accepts a manual IP).
//
// Safety: the relay never originates a byte. Everything it sends to the
// console is a datagram or stream bytes the app just sent, and everything it
// sends to the app came from the console. It opens one upstream socket per
// app endpoint, so the console sees as many clients as there are apps.
//
// Order: every payload is recorded before it is forwarded. The receiver
// cannot react to bytes it has not been sent yet, so the capture is in
// causal order: a request is always recorded before the reply it causes, and
// a reply before the app's next request. A payload whose forward then fails
// stays in the capture; the failure is logged.
//
// Exposure: anyone who can reach the listen address can reach the console
// through the relay. Callers bind one local address and should pass the
// vendor app's address as an allow-list (Apps); other sources are dropped
// before anything is forwarded.
package relay

import (
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"time"
)

// Defaults and limits.
const (
	DefaultUDPIdle = 60 * time.Second
	DefaultMaxApps = 32
	readBuffer     = 64 << 10
)

// Recorder receives every forwarded payload with its logical endpoints:
// app -> console or console -> app. Implemented by session.Recorder.
type Recorder interface {
	Datagram(at time.Time, src, dst netip.AddrPort, payload []byte) error
	Stream(at time.Time, src, dst netip.AddrPort, payload []byte) error
}

// ErrClosed is returned by Serve after its context ends.
var ErrClosed = errors.New("relay: closed")

func discard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l
}

// allowed reports whether a may use the relay: every source when apps is
// empty, else only the listed addresses.
func allowed(apps []netip.Addr, a netip.Addr) bool {
	return len(apps) == 0 || slices.Contains(apps, a.Unmap())
}

func unmapped(ap netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}
