// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package x32 drives Behringer X32 / Midas M32 family consoles over OSC/UDP
// (compatibility statement only). It is implemented from
// docs/protocols/x32.md; comments cite fact IDs such as X32-F20.
//
// Echo behaviour: the console does not push a change back to the client that
// sent it (X32-F23), so the session keeps two sockets. The subscription
// socket holds /xremote, sends reads and receives pushes and meters; the
// command socket only sends writes. Our own writes therefore come back on the
// subscription socket and are reported as ParamChanged with the origin of the
// write (EchoesOwnWrites = true). Nothing in this package writes in response
// to a received packet.
//
// Sockets: OpenLink opens both sockets once; every session of a supervisor
// runs over them, so reconnects never change our source ports (the console
// counts each new port as a new client, X32-F28).
//
// Client limit: four /xremote clients (X32-F21). The console says nothing
// when we are not served; three unechoed value-changing writes, or two
// re-read passes with unpushed changes, raise ClientLimitHit; any push
// clears it (ClientLimitCleared). Subscriptions last 10 s and
// are renewed every Timing.RenewEvery. Liveness (X32-F25): any packet from the
// console counts; after StaleAfter without one the session is lost. A quiet
// console is probed with /xinfo after QuietProbeAfter, and one /xinfo per
// PingEvery checks the uplink (lost after DeadAfter without a reply).
//
// Background re-read (driver.Rereader): on request, one read-only pass over
// every parameter, paced at one batch of reads per PingEvery, recovers changes
// whose push was lost. Only values that differ from the last one seen are
// reported; parameters we wrote within EchoWindow are skipped and replies that
// cross one of our writes are dropped.
package x32

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/model"
)

// Family is the family name of this driver.
const Family driver.Family = "x32"

// Timing holds every interval of the session. Defaults are documented in
// docs/protocols/x32.md (X32-F20, X32-F25, X32-F27, X32-F50); tests scale them down.
type Timing struct {
	RenewEvery      time.Duration // /xremote and /meters renewal, <= TTL/3 (X32-F27)
	PingEvery       time.Duration // uplink /xinfo probe and re-read pacing (X32-F25)
	DeadAfter       time.Duration // no /xinfo reply for this long: the uplink is lost (X32-F25)
	StaleAfter      time.Duration // no packet at all for this long: the console is lost (X32-F25, SR-05)
	QuietProbeAfter time.Duration // send /xinfo when nothing arrived for this long (X32-F25)
	LivenessTick    time.Duration // how often the liveness rules above are checked
	ConnectTimeout  time.Duration // identify + full initial read
	ReadRetryEvery  time.Duration // re-send unanswered reads and /xinfo during connect (X32-F07)
	EchoWindow      time.Duration // how long a sent value is matched against pushes
}

// DefaultTiming is used for every zero field of Config.Timing.
var DefaultTiming = Timing{
	RenewEvery:      3 * time.Second,
	PingEvery:       time.Second,
	DeadAfter:       3 * time.Second,
	StaleAfter:      1500 * time.Millisecond,
	QuietProbeAfter: 400 * time.Millisecond,
	LivenessTick:    100 * time.Millisecond,
	ConnectTimeout:  5 * time.Second,
	ReadRetryEvery:  300 * time.Millisecond,
	EchoWindow:      2 * time.Second,
}

// withDefaults fills every zero field from DefaultTiming.
func (t Timing) withDefaults() Timing {
	or := func(v, def time.Duration) time.Duration {
		if v == 0 {
			return def
		}
		return v
	}
	d := DefaultTiming
	return Timing{
		RenewEvery: or(t.RenewEvery, d.RenewEvery), PingEvery: or(t.PingEvery, d.PingEvery),
		DeadAfter: or(t.DeadAfter, d.DeadAfter), StaleAfter: or(t.StaleAfter, d.StaleAfter),
		QuietProbeAfter: or(t.QuietProbeAfter, d.QuietProbeAfter), LivenessTick: or(t.LivenessTick, d.LivenessTick),
		ConnectTimeout: or(t.ConnectTimeout, d.ConnectTimeout), ReadRetryEvery: or(t.ReadRetryEvery, d.ReadRetryEvery),
		EchoWindow: or(t.EchoWindow, d.EchoWindow),
	}
}

// Config configures the driver.
type Config struct {
	Timing Timing
}

// Driver implements driver.Driver for the X32 family.
type Driver struct {
	timing Timing
	params paramTable
}

var _ driver.Driver = (*Driver)(nil)

// ErrBadEndpoint is returned for an endpoint that is not an IP address.
var ErrBadEndpoint = errors.New("x32: endpoint must be an IP address")

// New returns a driver. Zero fields of Config.Timing take DefaultTiming.
func New(cfg Config) *Driver {
	return &Driver{timing: cfg.Timing.withDefaults(), params: newParamTable(inputChannels)}
}

// Family implements driver.Driver.
func (d *Driver) Family() driver.Family { return Family }

// FaderLaw implements driver.Driver (X32-F40).
func (d *Driver) FaderLaw() driver.FaderLaw { return faderLaw{} }

// Capabilities of every X32-family console as far as this driver supports them.
func capabilities() model.Capabilities {
	return model.Capabilities{
		InputCount: inputChannels, HasMain: true, InputMeters: true,
		EchoesOwnWrites: true, MaxPushClients: maxPushClients,
	}
}

// Connect implements driver.Driver. It identifies the console with /xinfo,
// registers /xremote and reads every parameter. It never writes a parameter.
// The session owns its sockets and closes them on Close; a supervisor uses
// OpenLink instead so that reconnects keep the same sockets (X32-F28).
func (d *Driver) Connect(ctx context.Context, ep driver.Endpoint, opts driver.Options) (_ driver.Session, err error) {
	defer safe.RecoverAsError(&err)
	l, err := d.openLink(ctx, ep, opts)
	if err != nil {
		return nil, err
	}
	sess, err := l.Connect(ctx)
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	s := sess.(*session)
	s.ownLink = l
	return s, nil
}

func resolve(ep driver.Endpoint) (netip.AddrPort, error) {
	ip, err := netip.ParseAddr(ep.Host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: %q", ErrBadEndpoint, ep.Host)
	}
	port := ep.Port
	if port == 0 {
		port = DefaultPort
	}
	if port < 1 || port > 65535 {
		return netip.AddrPort{}, fmt.Errorf("%w: port %d", ErrBadEndpoint, port)
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), nil
}

func listenFunc(opts driver.Options) driver.ListenPacketFunc {
	if opts.ListenPacket != nil {
		return opts.ListenPacket
	}
	return func(ctx context.Context, network, address string) (net.PacketConn, error) {
		var lc net.ListenConfig
		return lc.ListenPacket(ctx, network, address)
	}
}

func logger(opts driver.Options) *slog.Logger {
	if opts.Logger != nil {
		return opts.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// anyAddr is the unconnected local address both sockets bind to.
var anyAddr = net.JoinHostPort("0.0.0.0", strconv.Itoa(0))
