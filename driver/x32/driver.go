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
// Client limit: four /xremote clients (X32-F21). Subscriptions last 10 s and
// are renewed every Timing.RenewEvery. Liveness is the time of the last packet
// received, kept fresh by an /xinfo poll (X32-F25). A scene recall arrives as
// ordinary pushes (no StateReplaced yet).
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
	RenewEvery     time.Duration // /xremote and /meters renewal, <= TTL/3 (X32-F27)
	PingEvery      time.Duration // /xinfo liveness poll (X32-F25)
	DeadAfter      time.Duration // silence after which the console is lost (X32-F25)
	ConnectTimeout time.Duration // identify + full initial read
	ReadRetryEvery time.Duration // re-send unanswered reads and /xinfo during connect (X32-F07)
	EchoWindow     time.Duration // how long a sent value is matched against pushes
}

// DefaultTiming is used when Config.Timing is zero.
var DefaultTiming = Timing{
	RenewEvery:     3 * time.Second,
	PingEvery:      time.Second,
	DeadAfter:      3 * time.Second,
	ConnectTimeout: 5 * time.Second,
	ReadRetryEvery: 300 * time.Millisecond,
	EchoWindow:     2 * time.Second,
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

// New returns a driver. A zero Config.Timing means DefaultTiming.
func New(cfg Config) *Driver {
	t := cfg.Timing
	if t == (Timing{}) {
		t = DefaultTiming
	}
	return &Driver{timing: t, params: newParamTable(inputChannels)}
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
func (d *Driver) Connect(ctx context.Context, ep driver.Endpoint, opts driver.Options) (_ driver.Session, err error) {
	defer safe.RecoverAsError(&err)
	console, err := resolve(ep)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, d.timing.ConnectTimeout)
	defer cancel()
	c, err := openConn(ctx, console, opts)
	if err != nil {
		return nil, err
	}
	s := newSession(d, c, opts)
	defer func() {
		if err != nil {
			_ = s.Close() // also after a recovered panic
		}
	}()
	if err := s.connect(ctx); err != nil {
		return nil, fmt.Errorf("x32: connect %s: %w", console, err)
	}
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
