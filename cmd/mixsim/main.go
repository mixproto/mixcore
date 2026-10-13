// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command mixsim serves a console simulator on real UDP ports for manual and
// end-to-end tests. It is a test tool, not a console.
//
//	mixsim -listen 127.0.0.1:10023 -name X32-SIM -demo
//	mixsim -family xair -demo                 # X Air on 127.0.0.1:10024
//	mixsim -family wing -demo                 # WING on 127.0.0.1:2222, TCP and UDP
//
// Use -listen 0.0.0.0:<port> to reach it from a phone on the same LAN.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	simwing "github.com/mixproto/mixcore/sim/wing"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

const (
	demoTick   = 50 * time.Millisecond
	demoPeriod = 4.0 // seconds per meter swing
	familyX32  = "x32"
	familyXAir = "xair"
	familyWing = "wing"
)

var errFamily = errors.New("-family must be x32, xair or wing")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mixsim:", err)
		os.Exit(1)
	}
}

// flags are the parsed command line. Empty identity flags take the
// simulator's documented defaults.
type flags struct {
	family, listen, name, model, firmware string
	demo, verbose, pushSnapIndex          bool
	wing                                  wingOptions
}

func parseFlags() (flags, error) {
	var f flags
	flag.StringVar(&f.family, "family", familyX32, "console family: x32, xair or wing")
	flag.StringVar(&f.listen, "listen", "", "address to serve on (default 127.0.0.1:<family port>; wing: TCP and UDP)")
	flag.StringVar(&f.name, "name", "", "network name reported by /xinfo")
	flag.StringVar(&f.model, "model", "", "model reported by /xinfo and /info")
	flag.StringVar(&f.firmware, "firmware", "", "firmware version reported")
	flag.BoolVar(&f.demo, "demo", false, "fill channel names, name six monitor buses with sends, animate input meters")
	flag.BoolVar(&f.pushSnapIndex, "push-snap-index", false, "xair: push /-snap/index on a snapshot load (unverified, XA-F62)")
	flag.BoolVar(&f.wing.echo, "wing-echo", false, "wing: push a client's own write back to it (unverified, item 4)")
	flag.BoolVar(&f.wing.dataDE, "wing-data-de", false, "wing: end every data answer with DE (unverified, item 3)")
	flag.BoolVar(&f.wing.markers, "wing-scene-markers", false, "wing: push $actidx/$active on a scene load (unverified, item 17)")
	flag.IntVar(&f.wing.maxConns, "wing-max-conns", 0, "wing: TCP connection limit (default 16, 24 from firmware 3.1)")
	flag.BoolVar(&f.verbose, "v", false, "debug logging")
	flag.Parse()
	ports := map[string]int{familyX32: simx32.DefaultPort, familyXAir: simxair.DefaultPort, familyWing: simwing.DefaultPort}
	port, ok := ports[f.family]
	if !ok {
		return f, errFamily
	}
	if f.listen == "" {
		f.listen = fmt.Sprintf("127.0.0.1:%d", port)
	}
	return f, nil
}

// server is the part of a family simulator mixsim needs.
type server interface {
	Serve(ctx context.Context, pc net.PacketConn) error
	WriteCount() int
}

func run() error {
	f, err := parseFlags()
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if f.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if f.family == familyWing {
		return runWing(ctx, f, log)
	}
	pc, err := net.ListenPacket("udp4", f.listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", f.listen, err)
	}
	defer pc.Close()
	sim, err := newServer(ctx, f, log)
	if err != nil {
		return err
	}
	log.Info("mixsim: serving", "family", f.family, "addr", pc.LocalAddr().String())
	if err := sim.Serve(ctx, pc); err != nil {
		return err
	}
	log.Info("mixsim: stopped", "writes_received", sim.WriteCount())
	return nil
}

func newServer(ctx context.Context, f flags, log *slog.Logger) (server, error) {
	if f.family == familyXAir {
		sim := simxair.New(simxair.Options{Name: f.name, Model: f.model, Firmware: f.firmware, PushSnapIndex: f.pushSnapIndex, Logger: log})
		if f.demo {
			if err := fillXAirDemo(sim); err != nil {
				return nil, err
			}
			go animateXAir(ctx, sim)
		}
		return sim, nil
	}
	sim := simx32.New(simx32.Options{Name: cmpOr(f.name, "X32-SIM"), Model: cmpOr(f.model, "X32"), Firmware: cmpOr(f.firmware, "4.06"), Logger: log})
	if f.demo {
		if err := fillDemoNames(sim); err != nil {
			return nil, err
		}
		go animateMeters(ctx, sim)
	}
	return sim, nil
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
