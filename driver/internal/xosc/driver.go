// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package xosc is the session engine shared by the console families that
// speak the X32-style OSC dialect over UDP: driver/x32 (docs/protocols/x32.md)
// and driver/xair (docs/protocols/xair.md, which section 11 and 12 say what
// is the same). A family supplies a Profile: its port, address layout, meter
// codec, scene markers and push-client limit; everything else is here and
// cites the X32 fact IDs, which xair.md confirms for X Air.
//
// Echo: the session keeps two sockets (X32-F23, XA section 2 "Echo design").
// The subscription socket holds /xremote, sends reads and receives pushes and
// meters; the command socket sends writes and background re-reads. Our own
// writes come back as pushes on the subscription socket and are reported as
// ParamChanged with the origin of the write (EchoesOwnWrites = true). A
// packet on the command socket is accepted only as the reply to an
// outstanding, untouched re-read, so an echo of a write to its sender (X Air
// hypothesis H3) is dropped. Nothing in this package writes in response to a
// received packet.
//
// Sockets: OpenLink opens both sockets once; every session of a supervisor
// runs over them, so reconnects never change our source ports (X32-F28).
//
// Client limit (X32-F21, XA-F21): the console says nothing when we are not
// served; three unechoed value-changing writes, or two re-read passes with
// unpushed changes, raise ClientLimitHit; any push clears it. Liveness
// (X32-F25, XA-F25): any packet counts; after StaleAfter without one the
// session is lost; /xinfo probes the uplink.
//
// Scene recall: no dedicated message exists; a burst of console pushes and
// the family's scene markers raise SceneRecalled with a confidence, and a
// background re-read starts at once.
package xosc

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

// Timing holds every interval of the session. Defaults are documented in
// docs/protocols/x32.md (X32-F20, X32-F25, X32-F27, X32-F50) and kept for X Air
// (XA-F20, XA-F25); tests scale them down.
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
	// RereadGap: a requested background re-read starts no earlier than this
	// after the previous pass ended, so passes never run back to back (lead
	// decision 2026-10-12). Recovery passes (scene recall, served again)
	// start at once.
	RereadGap time.Duration
}

// DefaultTiming is used for every zero field of a Timing.
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
	RereadGap:       15 * time.Second,
}

// WithDefaults fills every zero field from DefaultTiming.
func (t Timing) WithDefaults() Timing {
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
		EchoWindow: or(t.EchoWindow, d.EchoWindow), RereadGap: or(t.RereadGap, d.RereadGap),
	}
}

// Profile is what a family adds to the engine. It is immutable once the
// driver is built.
type Profile struct {
	Family         driver.Family
	DefaultPort    int
	MaxPushClients int
	Layout         Layout
	Meters         MeterCodec
	// Markers are the scene markers the console may push (X32-F61, X32-F62,
	// XA-F60..F62); empty: the burst is the only signal.
	Markers []Marker
	// BadEndpoint is the family's error for an endpoint that is not an IP address.
	BadEndpoint error
	// TapLabels are the console's words for the send taps, in model.Tap
	// order (X32-F85; xair.md 3.3).
	TapLabels [model.TapCount]string
}

// Driver implements driver.Driver and driver.LinkOpener for one family.
type Driver struct {
	prof   Profile
	timing Timing
	params paramTable
}

var (
	_ driver.Driver     = (*Driver)(nil)
	_ driver.LinkOpener = (*Driver)(nil)
)

// New returns a driver for prof. Zero fields of timing take DefaultTiming.
func New(prof Profile, timing Timing) *Driver {
	return &Driver{prof: prof, timing: timing.WithDefaults(), params: newParamTable(prof.Layout)}
}

// Family implements driver.Driver.
func (d *Driver) Family() driver.Family { return d.prof.Family }

// FaderLaw implements driver.Driver (X32-F40, XA-F40: the same law).
func (d *Driver) FaderLaw() driver.FaderLaw { return faderLaw{} }

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

func (d *Driver) resolve(ep driver.Endpoint) (netip.AddrPort, error) {
	ip, err := netip.ParseAddr(ep.Host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: %q", d.badEndpoint(), ep.Host)
	}
	port := ep.Port
	if port == 0 {
		port = d.prof.DefaultPort
	}
	if port < 1 || port > 65535 {
		return netip.AddrPort{}, fmt.Errorf("%w: port %d", d.badEndpoint(), port)
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), nil
}

func (d *Driver) badEndpoint() error {
	if d.prof.BadEndpoint != nil {
		return d.prof.BadEndpoint
	}
	return errors.New(string(d.prof.Family) + ": endpoint must be an IP address")
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
