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
	// Monitor sends (X32-F82, X32-F83; xair.md 3.3): "more me" at 0 dB (0.75),
	// the rest at -10 dB (0.5), both on the 161-step grid; tap 3 = PRE in
	// both families (X32-F85, xair.md 3.3).
	sendMe   = 0.75
	sendRest = 0.5
	tapPre   = 3
)

// demoBuses names the first six mix buses (X32-F80, xair.md 3.1).
var demoBuses = [...]string{"Drums IEM", "Keys IEM", "Bass IEM", "Vox wedge", "Gtr wedge", "Pastor"}

// busFamily is where a family keeps buses and send taps.
type busFamily struct {
	root       func(b int) string // X32-F80 /bus/01; xair.md 3.1 /bus/1
	tapLeaf    string             // X32-F85 "type"; xair.md 3.3 "tap"
	tapOddOnly bool               // X32-F85: only odd sends, for the pair
}

var (
	x32Buses  = busFamily{root: func(b int) string { return fmt.Sprintf("/bus/%02d", b) }, tapLeaf: "type", tapOddOnly: true}
	xairBuses = busFamily{root: func(b int) string { return fmt.Sprintf("/bus/%d", b) }, tapLeaf: "tap"}
)

// fillBuses names the demo buses, sets their faders to unity and sends
// every input pre-fader into each: its own bus louder ("more me").
func fillBuses(sim surfaceSetter, inputs int, fam busFamily) error {
	for i, name := range demoBuses {
		b := i + 1
		if err := sim.SurfaceSet(fam.root(b)+"/config/name", osc.String(name)); err != nil {
			return err
		}
		if err := sim.SurfaceSet(fam.root(b)+"/mix/fader", osc.Float(unityLevel)); err != nil {
			return err
		}
		for ch := 1; ch <= inputs; ch++ {
			if err := fillSend(sim, fmt.Sprintf("/ch/%02d/mix/%02d", ch, b), (ch-1)%len(demoBuses) == i, fam, b); err != nil {
				return err
			}
		}
	}
	return nil
}

func fillSend(sim surfaceSetter, send string, me bool, fam busFamily, b int) error {
	level := float32(sendRest)
	if me {
		level = sendMe
	}
	if err := sim.SurfaceSet(send+"/level", osc.Float(level)); err != nil {
		return err
	}
	if fam.tapOddOnly && b%2 == 0 {
		return nil
	}
	return sim.SurfaceSet(send+"/"+fam.tapLeaf, osc.Int(tapPre))
}

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
	if err := fillNames(sim, demoX32Channels, "/main/st/mix/fader"); err != nil {
		return err
	}
	return fillBuses(sim, demoX32Channels, x32Buses)
}

func fillXAirDemo(sim *simxair.Sim) error {
	if err := fillNames(sim, demoXAirChannels, "/lr/mix/fader"); err != nil {
		return err
	}
	return fillBuses(sim, demoXAirChannels, xairBuses)
}

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
