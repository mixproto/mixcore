// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command mixsim serves a console simulator on real UDP ports for manual and
// end-to-end tests. It is a test tool, not a console.
//
//	mixsim -listen 127.0.0.1:10023 -name X32-SIM -demo
//
// Use -listen 0.0.0.0:10023 to reach it from a phone on the same LAN.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const (
	demoTick     = 50 * time.Millisecond
	demoChannels = 32
	demoPeriod   = 4.0 // seconds per meter swing
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mixsim:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", fmt.Sprintf("127.0.0.1:%d", simx32.DefaultPort), "UDP address to serve on")
	name := flag.String("name", "X32-SIM", "network name reported by /xinfo")
	model := flag.String("model", "X32", "model reported by /xinfo and /info")
	firmware := flag.String("firmware", "4.06", "firmware version reported")
	demo := flag.Bool("demo", false, "fill channel names and animate input meters")
	verbose := flag.Bool("v", false, "debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pc, err := net.ListenPacket("udp4", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	defer pc.Close()

	sim := simx32.New(simx32.Options{Name: *name, Model: *model, Firmware: *firmware, Logger: log})
	if *demo {
		if err := fillDemoNames(sim); err != nil {
			return err
		}
		go animateMeters(ctx, sim)
	}
	log.Info("mixsim: serving", "family", "x32", "addr", pc.LocalAddr().String(), "name", *name, "model", *model)
	if err := sim.Serve(ctx, pc); err != nil {
		return err
	}
	log.Info("mixsim: stopped", "writes_received", len(sim.Writes()))
	return nil
}

func fillDemoNames(sim *simx32.Sim) error {
	for ch := 1; ch <= demoChannels; ch++ {
		if err := sim.SurfaceSet(fmt.Sprintf("/ch/%02d/config/name", ch), osc.String(fmt.Sprintf("Input %d", ch))); err != nil {
			return err
		}
		if err := sim.SurfaceSet(fmt.Sprintf("/ch/%02d/mix/fader", ch), osc.Float(0.75)); err != nil {
			return err
		}
	}
	return sim.SurfaceSet("/main/st/mix/fader", osc.Float(0.75))
}

// animateMeters sweeps each input meter with a phase offset so a UI shows movement.
func animateMeters(ctx context.Context, sim *simx32.Sim) {
	t := time.NewTicker(demoTick)
	defer t.Stop()
	start := time.Now()
	levels := make([]float32, demoChannels)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			phase := now.Sub(start).Seconds() * 2 * math.Pi / demoPeriod
			for i := range levels {
				levels[i] = float32(0.5 + 0.5*math.Sin(phase+float64(i)*0.3))
			}
			sim.SetInputLevels(levels)
			sim.SetMainLevels(levels[0], levels[1])
		}
	}
}
