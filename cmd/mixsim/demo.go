// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

const (
	demoX32Channels  = 32
	demoXAirChannels = 16
	unityLevel       = 0.75 // 0 dB on both families (X32-F43, XA-F40)
	// xairDBSteps: X Air meters are int16 in 1/256 dB (XA-F51).
	xairDBSteps = 256
	demoFloorDB = -60.0
)

// surfaceSetter is SurfaceSet of either simulator.
type surfaceSetter interface {
	SurfaceSet(addr string, a osc.Arg) error
}

// fillNames names channels 1..n "Input n" and sets them and main to unity.
func fillNames(sim surfaceSetter, n int, mainFader string) error {
	for ch := 1; ch <= n; ch++ {
		if err := sim.SurfaceSet(fmt.Sprintf("/ch/%02d/config/name", ch), osc.String(fmt.Sprintf("Input %d", ch))); err != nil {
			return err
		}
		if err := sim.SurfaceSet(fmt.Sprintf("/ch/%02d/mix/fader", ch), osc.Float(unityLevel)); err != nil {
			return err
		}
	}
	return sim.SurfaceSet(mainFader, osc.Float(unityLevel))
}

func fillDemoNames(sim *simx32.Sim) error {
	return fillNames(sim, demoX32Channels, "/main/st/mix/fader")
}

func fillXAirDemo(sim *simxair.Sim) error { return fillNames(sim, demoXAirChannels, "/lr/mix/fader") }

// sweep returns n levels in 0..1 with a phase offset per channel.
func sweep(now, start time.Time, out []float64) {
	phase := now.Sub(start).Seconds() * 2 * math.Pi / demoPeriod
	for i := range out {
		out[i] = 0.5 + 0.5*math.Sin(phase+float64(i)*0.3)
	}
}

// animate calls apply with fresh levels on every demo tick until ctx ends.
func animate(ctx context.Context, n int, apply func([]float64)) {
	t := time.NewTicker(demoTick)
	defer t.Stop()
	start := time.Now()
	levels := make([]float64, n)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			sweep(now, start, levels)
			apply(levels)
		}
	}
}

// animateMeters sweeps each X32 input meter (linear, X32-F54) so a UI shows movement.
func animateMeters(ctx context.Context, sim *simx32.Sim) {
	lin := make([]float32, demoX32Channels)
	animate(ctx, demoX32Channels, func(levels []float64) {
		for i, l := range levels {
			lin[i] = float32(l)
		}
		sim.SetInputLevels(lin)
		sim.SetMainLevels(lin[0], lin[1])
	})
}

// animateXAir sweeps the X Air input meters and Main L/R between -60 and 0 dBFS.
func animateXAir(ctx context.Context, sim *simxair.Sim) {
	raw := make([]int16, demoXAirChannels)
	animate(ctx, demoXAirChannels, func(levels []float64) {
		for i, l := range levels {
			raw[i] = int16((1 - l) * demoFloorDB * xairDBSteps)
		}
		sim.SetMeterValues(raw)
		sim.SetMeterValue(simxair.Bank1MainL, raw[0])
		sim.SetMeterValue(simxair.Bank1MainL+1, raw[1])
	})
}
