// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package wing drives Behringer WING consoles (WING, WING BK, WING Rack,
// WING Compact; compatibility statement only) over the native binary
// protocol on TCP 2222, implemented from docs/protocols/wing.md only;
// comments cite fact IDs such as WG-F30. The codec is internal/wingproto.
//
// Transport (wing.md 2.1): one TCP connection per session, data on mux
// channel DF D1, meter requests on DF D3, meters back by UDP to a port of
// ours (WG-F10, WG-F14, WG-F50). OSC is never used: it has one subscriber
// per console, which mixcore would steal from other tools (WG-F21).
//
// Addressing: hashes (WG-F32). The session learns them from the console:
// every connect walks the nodes it models with definition requests only
// (D7 <hash> DD, WG-F31, WG-F33; about 280 requests), and nothing a session
// read is kept for the next (security re-check 1). A walk in which a path
// the document lists has another hash is refused. A node the console does
// not define is absent, never
// faked; laws (headamp gain) come from the console's own definition
// (wing.md 3.0), which also covers 3.0 versus 3.1 differences as data.
//
// Echo (WG-F24, unverified, item 4): whether the console sends our own
// write back is unknown, so the session works under both answers (wing.md
// 2.4). Every write is remembered per parameter for EchoWindow; a push with
// a value within half a step of the law is our echo (reported with the
// write's origin). SettleAfter after the last write to a parameter the
// session reads it back; the reply is Authoritative, and OwnWrite when it is
// the last value we sent, so the supervisor's in-flight Gain values always
// settle (console/pending.go), and a console that did not take the value
// (remote lock, another client) ends the held gesture. EchoesOwnWrites is true.
// Nothing is ever written in response to something received.
//
// Liveness (WG-F25, wing.md 2.4; review go M1, L6): any complete message
// counts (bytes of an unfinished token do not); the session reads
// /$syscfg/$firmware every PingEvery and after QuietProbeAfter of silence.
// After StaleAfter without a message it reports ConsoleSilent (input
// locked, queued writes dropped) and keeps the connection, because TCP on
// a lossy link recovers; answers end it with ConsoleResponding and a
// re-read. The connection is closed after DeadAfter, or as soon as the
// meter stream (when on) is silent too. The console's own 10 s idle
// timeout never comes close.
//
// Client limit (WG-F22): 16 TCP connections, 24 from firmware 3.1; further
// connections are refused, so there is no silent "not served" state and no
// ClientLimitHit. A refused dial is a connect error with backoff.
//
// Remote lock (WG-F26): /$syscfg/tcplock = 1 makes every Apply return
// ErrUnsupported until the console reports 0.
//
// Scene recall (wing.md section 7): a burst of at least 8 parameters pushed
// within 250 ms raises SceneRecalled (low) and an immediate re-read. Whether
// the console pushes /$ctl/lib/$actidx is unverified (item 17); it is not used.
//
// Background re-read: TCP does not lose pushes, so a slow pass (RereadGap,
// 60 s) only guards against changes the console does not push (item 5).
//
// Model mapping today: inputs /ch/1..40 (fader, mute, name, DCA membership
// from tags #D1..#D16, the headamp of the channel's source); main /main/1
// (open question 4: mains 2..4 are not modelled yet); DCAs 1..16 and mute
// groups 1..8 (mute only); headamps LCL 1..8 on WING and 1..24 on Rack and
// Compact (WG-F03) with writable Gain, AES50 A and B read-only (item 15),
// phantom read-only.
//
// Effective mute (review H1): each input's and main's $mute is read at
// connect, on re-reads and on every push (WG-F24) as the console's own
// report: 0 not muted, 1 muted, 2 unknown until item 12. Mute-group
// membership of strips is unknown (item 11): capabilities say so and the
// state's MuteRules make an unmuted strip unknown while a group is
// engaged, never live. DCA membership is reported as the tags say;
// /cfg/dcamgrp only changes the rule (MuteRulesChanged).
package wing

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
)

// Family is the family name of this driver.
const Family driver.Family = "wing"

// DefaultPort is the native control port, TCP 2222 (WG-F10). Discovery uses
// UDP 2222 (WG-F11).
const DefaultPort = 2222

// Timing holds every interval of the session. Defaults follow wing.md 2.4
// and section 6; tests scale them down.
type Timing struct {
	PingEvery       time.Duration // liveness probe interval (wing.md 2.4)
	QuietProbeAfter time.Duration // probe early after this much silence (wing.md 2.4)
	StaleAfter      time.Duration // no message for this long: input locked, ConsoleSilent (wing.md 2.4)
	DeadAfter       time.Duration // no message for this long (or StaleAfter with meters silent too): connection closed
	Tick            time.Duration // how often timers are checked
	ConnectTimeout  time.Duration // schema walk and initial read
	WalkQuiet       time.Duration // a definition level without DE ends after this silence (WG-F31, item 3)
	OptionalWait    time.Duration // after the required values, wait this long for the rest
	EchoWindow      time.Duration // a written value is matched against pushes this long (wing.md 2.4)
	SettleAfter     time.Duration // read a parameter back this long after its last write (wing.md 2.4)
	RereadGap       time.Duration // background re-read passes at most this often (wing.md 2.4)
	MeterRenewEvery time.Duration // meters last 5 s; renew at a third of it (WG-F51, wing.md 6)
	WriteTimeout    time.Duration // a TCP write that takes longer fails the session
}

// DefaultTiming is used for every zero field of Config.Timing.
var DefaultTiming = Timing{
	PingEvery:       time.Second,
	QuietProbeAfter: 400 * time.Millisecond,
	StaleAfter:      1500 * time.Millisecond,
	DeadAfter:       5 * time.Second,
	Tick:            25 * time.Millisecond,
	ConnectTimeout:  10 * time.Second,
	WalkQuiet:       time.Second,
	OptionalWait:    300 * time.Millisecond,
	EchoWindow:      2 * time.Second,
	SettleAfter:     150 * time.Millisecond,
	RereadGap:       60 * time.Second,
	MeterRenewEvery: 1500 * time.Millisecond,
	WriteTimeout:    time.Second,
}

func (t Timing) withDefaults() Timing {
	or := func(v, def time.Duration) time.Duration {
		if v <= 0 {
			return def
		}
		return v
	}
	d := DefaultTiming
	return Timing{
		PingEvery: or(t.PingEvery, d.PingEvery), QuietProbeAfter: or(t.QuietProbeAfter, d.QuietProbeAfter),
		StaleAfter: or(t.StaleAfter, d.StaleAfter), DeadAfter: or(t.DeadAfter, d.DeadAfter), Tick: or(t.Tick, d.Tick), ConnectTimeout: or(t.ConnectTimeout, d.ConnectTimeout),
		WalkQuiet: or(t.WalkQuiet, d.WalkQuiet), OptionalWait: or(t.OptionalWait, d.OptionalWait),
		EchoWindow: or(t.EchoWindow, d.EchoWindow), SettleAfter: or(t.SettleAfter, d.SettleAfter),
		RereadGap: or(t.RereadGap, d.RereadGap), MeterRenewEvery: or(t.MeterRenewEvery, d.MeterRenewEvery),
		WriteTimeout: or(t.WriteTimeout, d.WriteTimeout),
	}
}

// Config configures the driver.
type Config struct {
	Timing Timing
}

// Driver implements driver.Driver and driver.LinkOpener for the WING family.
type Driver struct {
	timing Timing
}

var (
	_ driver.Driver     = (*Driver)(nil)
	_ driver.LinkOpener = (*Driver)(nil)
)

// New returns a driver. Zero fields of Config.Timing take DefaultTiming.
func New(cfg Config) *Driver { return &Driver{timing: cfg.Timing.withDefaults()} }

// Family implements driver.Driver.
func (d *Driver) Family() driver.Family { return Family }

// FaderLaw implements driver.Driver: the display law (law.go). WING writes
// dB on the wire (WG-F60), so the law never reaches the console.
func (d *Driver) FaderLaw() driver.FaderLaw { return faderLaw{} }

// Connect implements driver.Driver: it dials, walks the schema
// and reads every modelled parameter. It never writes a parameter. The
// session owns its sockets; a supervisor uses OpenLink instead.
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
		return netip.AddrPort{}, ErrBadEndpoint
	}
	port := ep.Port
	if port == 0 {
		port = DefaultPort
	}
	if port < 1 || port > 65535 {
		return netip.AddrPort{}, ErrBadEndpoint
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), nil
}

func dialFunc(opts driver.Options) driver.DialFunc {
	if opts.Dial != nil {
		return opts.Dial
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
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
