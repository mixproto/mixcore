// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	simwing "github.com/mixproto/mixcore/sim/wing"
)

const demoWingChannels = 40

// wingOptions are the WING flags (docs/protocols/wing.md, section 13 items).
type wingOptions struct {
	echo, dataDE, markers bool
	maxConns              int
}

// runWing serves sim/wing on TCP and UDP of one address: control on TCP,
// discovery and meters on UDP (WG-F10, WG-F11, WG-F51).
func runWing(ctx context.Context, f flags, log *slog.Logger) error {
	ln, err := net.Listen("tcp4", f.listen)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", f.listen, err)
	}
	pc, err := net.ListenPacket("udp4", f.listen)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("listen udp %s: %w", f.listen, err)
	}
	defer pc.Close()
	sim, err := newWing(ctx, f, log)
	if err != nil {
		_ = ln.Close()
		return err
	}
	log.Info("mixsim: serving", "family", f.family, "tcp", ln.Addr().String(), "udp", pc.LocalAddr().String())
	var wg sync.WaitGroup
	var udpErr error
	wg.Go(func() { udpErr = sim.ServeUDP(ctx, pc) })
	tcpErr := sim.Serve(ctx, ln)
	wg.Wait()
	log.Info("mixsim: stopped", "writes_received", sim.WriteCount())
	return errors.Join(tcpErr, udpErr)
}

func newWing(ctx context.Context, f flags, log *slog.Logger) (*simwing.Sim, error) {
	sim := simwing.New(simwing.Options{Name: cmpOr(f.name, "WING-SIM"), Model: cmpOr(f.model, "ngc-full"),
		Firmware: f.firmware, EchoToWriter: f.wing.echo, DataEndsWithDE: f.wing.dataDE,
		PushSceneMarkers: f.wing.markers, MaxConnections: f.wing.maxConns, Logger: log})
	if !f.demo {
		return sim, nil
	}
	for ch := 1; ch <= demoWingChannels; ch++ {
		if err := sim.SurfaceSet(fmt.Sprintf("/ch/%d/name", ch), fmt.Sprintf("Input %d", ch)); err != nil {
			return nil, err
		}
		if err := sim.SurfaceSet(fmt.Sprintf("/ch/%d/fdr", ch), 0.0); err != nil {
			return nil, err
		}
	}
	if err := sim.SurfaceSet("/main/1/fdr", 0.0); err != nil {
		return nil, err
	}
	go animateWing(ctx, sim)
	return sim, nil
}

// animateWing sweeps the WING input meters and Main L/R between -60 and 0 dBFS.
func animateWing(ctx context.Context, sim *simwing.Sim) {
	animate(ctx, demoWingChannels, func(levels []float64) {
		for i, l := range levels {
			sim.SetInputLevel(i+1, (1-l)*demoFloorDB)
		}
		sim.SetMainLevels((1-levels[0])*demoFloorDB, (1-levels[1])*demoFloorDB)
	})
}
