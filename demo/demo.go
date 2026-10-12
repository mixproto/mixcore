// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package demo is the built-in demo mixer: a console simulator (sim/x32 or
// sim/xair, the drivers' test oracles) running inside the app on a UDP socket
// bound to 127.0.0.1, seeded with a plausible show and with meters that move
// like music. An app connects to it with a normal engine session, so every
// screen, gesture, echo, meter and safety rule behaves as with a console
// (contracts/core-api README, "Demo mixer").
//
// Rules kept here: nothing runs before Start or after Stop; the demo's socket
// and the sessions opened through Listen talk to loopback addresses only; the
// demo writes to its own simulator as the "desk" would, never as a client.
package demo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
)

// Defaults of Config.
const (
	// DefaultMeterTick matches the simulators' meter interval (X32-F50, XA-F56).
	DefaultMeterTick = 50 * time.Millisecond
	// DefaultDeskChangeEvery is how often a fader moves at the desk when
	// Config.DeskChanges is set.
	DefaultDeskChangeEvery = 30 * time.Second
)

// Errors of Start.
var (
	ErrFamily = errors.New("demo: no demo mixer for this family")
	ErrListen = errors.New("demo: cannot open the loopback socket")
)

// Config configures Start. Zero durations take the defaults.
type Config struct {
	Family string          // FamilyX32 or FamilyXAir
	Law    driver.FaderLaw // the family's fader law, to seed levels in dB
	// DeskChanges moves one fader every DeskChangeEvery as the desk would.
	DeskChanges     bool
	DeskChangeEvery time.Duration
	MeterTick       time.Duration
	// ListenPacket opens the demo's socket (wrapped by Listen); nil: the OS.
	ListenPacket driver.ListenPacketFunc
	Logger       *slog.Logger // nil: discard
}

// Mixer is a running demo mixer. Stop it when the app leaves the demo.
type Mixer struct {
	family   string
	endpoint netip.AddrPort
	pc       net.PacketConn
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// Start opens the loopback socket, seeds the simulator and starts its two
// goroutines (the simulator and the animator). It never blocks on the
// network beyond opening the socket.
func Start(cfg Config) (*Mixer, error) {
	fam, ok := families[cfg.Family]
	if !ok || cfg.Law == nil {
		return nil, fmt.Errorf("%w: %q", ErrFamily, cfg.Family)
	}
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	pc, err := Listen(cfg.ListenPacket)(ctx, "udp4", net.JoinHostPort(loopbackIP, "0"))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %w", ErrListen, err)
	}
	sim, out, err := newConsole(cfg.Family, cfg.Law, cfg.Logger)
	if err != nil {
		cancel()
		_ = pc.Close()
		return nil, err
	}
	ua, _ := pc.LocalAddr().(*net.UDPAddr) // Listen returns loopback UDP sockets only
	ap := ua.AddrPort()
	m := &Mixer{family: cfg.Family, endpoint: netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), pc: pc, cancel: cancel}
	onPanic := func(err error) { cfg.Logger.Error("demo: goroutine panicked", "err", err) }
	safe.Go(&m.wg, onPanic, func() {
		if err := sim.Serve(ctx, pc); err != nil {
			cfg.Logger.Error("demo: simulator stopped", "err", err)
		}
	})
	anim := newAnimator(sim, cfg.Law, fam, out, cfg.Logger)
	safe.Go(&m.wg, onPanic, func() { anim.run(ctx, cfg.MeterTick, cfg.deskEvery()) })
	cfg.Logger.Info("demo: mixer started", "family", cfg.Family, "addr", m.endpoint.String())
	return m, nil
}

func (c Config) withDefaults() Config {
	if c.MeterTick <= 0 {
		c.MeterTick = DefaultMeterTick
	}
	if c.DeskChangeEvery <= 0 {
		c.DeskChangeEvery = DefaultDeskChangeEvery
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return c
}

func (c Config) deskEvery() time.Duration {
	if !c.DeskChanges {
		return 0
	}
	return c.DeskChangeEvery
}

// Family is the demo's console family.
func (m *Mixer) Family() string { return m.family }

// Endpoint is where the demo listens: 127.0.0.1 and a port the OS picked.
func (m *Mixer) Endpoint() netip.AddrPort { return m.endpoint }

// Name is the console name the simulator reports.
func (m *Mixer) Name() string { return consoleName }

// Stop ends both goroutines and closes the socket, freeing the port.
// Idempotent; returns when everything has stopped.
func (m *Mixer) Stop() {
	m.stopOnce.Do(func() {
		m.cancel()
		m.wg.Wait()
		_ = m.pc.Close()
	})
}
