// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"net/netip"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/internal/xosc"
)

// Found is one console that answered a discovery probe.
type Found = xosc.Found

// LimitedBroadcast is the all-hosts broadcast target on the control port (X32-F12).
var LimitedBroadcast = xosc.LimitedBroadcast(DefaultPort)

// BroadcastTargets returns the limited broadcast address plus the directed
// broadcast address of every up, broadcast-capable IPv4 interface on the X32
// port, so that a probe reaches consoles on every network the device is on.
func BroadcastTargets() []netip.AddrPort { return xosc.BroadcastTargets(DefaultPort) }

// Probe sends /xinfo to every target from one unconnected socket and collects
// replies until wait has passed or ctx ends (X32-F12). It only reads. Targets
// carry their own port, so one probe may also reach other families; callers
// classify replies by port and model (IsModel).
func Probe(ctx context.Context, listen driver.ListenPacketFunc, targets []netip.AddrPort, wait time.Duration) ([]Found, error) {
	return xosc.Probe(ctx, listen, targets, wait)
}
