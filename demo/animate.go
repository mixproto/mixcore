// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
)

// mainCeilingDB caps Main L/R: the demo never shows its own clip.
const mainCeilingDB = 0.0

// stripAddrs are the addresses the animator reads per input, built once.
type stripAddrs struct{ fader, on, groups, dcas, pan string }

// stripMix is what the main mix needs of one input this tick.
type stripMix struct {
	faderDB float64
	audible bool // on, and not muted by an engaged mute group or a muted DCA
	pan     float64
}

// animator moves the meters and, when asked, a fader at the desk. It runs
// on one goroutine; every buffer is allocated once.
type animator struct {
	sim        console
	law        driver.FaderLaw
	fam        family
	out        meterOut
	log        *slog.Logger
	addrs      []stripAddrs
	groupAddrs []string // /config/mute/N (X32-F35)
	dcaAddrs   []string // /dca/N/on (X32-F37; xair.md section 3.2)
	levels     []float64
	mix        []stripMix
	deskNext   int
}

func newAnimator(sim console, law driver.FaderLaw, fam family, out meterOut, log *slog.Logger) *animator {
	a := &animator{sim: sim, law: law, fam: fam, out: out, log: log,
		addrs: make([]stripAddrs, fam.inputs), levels: make([]float64, fam.inputs), mix: make([]stripMix, fam.inputs)}
	for i := range a.addrs {
		root := chRoot(i + 1)
		a.addrs[i] = stripAddrs{fader: root + "/mix/fader", on: root + "/mix/on", groups: root + "/grp/mute", dcas: root + "/grp/dca"}
		if fam.hasPan {
			a.addrs[i].pan = root + "/mix/pan"
		}
	}
	for g := 1; g <= fam.muteGroups; g++ {
		a.groupAddrs = append(a.groupAddrs, fmt.Sprintf("/config/mute/%d", g))
	}
	for d := 1; d <= fam.dcas; d++ {
		a.dcaAddrs = append(a.dcaAddrs, fmt.Sprintf("/dca/%d/on", d))
	}
	return a
}

// run ticks until ctx ends. deskEvery 0 disables desk changes. It starts
// half a tick after the simulator's meter loop (same period), so a fresh
// level always lands between two packets: no packet repeats the previous one.
func (a *animator) run(ctx context.Context, tick, deskEvery time.Duration) {
	phase := time.NewTimer(tick / 2)
	select {
	case <-ctx.Done():
		phase.Stop()
		return
	case <-phase.C:
	}
	meters := time.NewTicker(tick)
	defer meters.Stop()
	var desk <-chan time.Time
	if deskEvery > 0 {
		t := time.NewTicker(deskEvery)
		defer t.Stop()
		desk = t.C
	}
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-meters.C:
			a.step(now.Sub(start).Seconds())
		case <-desk:
			a.deskChange()
		}
	}
}

// step publishes the meters of song time t.
func (a *animator) step(t float64) {
	for i := range a.levels {
		a.levels[i] = voiceLevel(channels[i].voice, t, i+1)
	}
	a.readMix()
	l, r := mainMix(a.levels, a.mix, a.mainDB())
	a.out.publish(a.levels, l, r)
}

// readMix reads faders, mutes, membership and pans as the console holds them now.
func (a *animator) readMix() {
	var engaged, dcaMuted uint8
	for g, addr := range a.groupAddrs {
		if a.intAt(addr) == 1 { // 1 = engaged (X32-F35)
			engaged |= 1 << g
		}
	}
	for d, addr := range a.dcaAddrs {
		if a.intAt(addr) == 0 { // 0 = DCA muted (X32-F37)
			dcaMuted |= 1 << d
		}
	}
	for i, ad := range a.addrs {
		pan := channels[i].pan
		if ad.pan != "" {
			pan = 2*a.floatAt(ad.pan) - 1 // 0..1 = L100..R100 (xair.md section 3.2)
		}
		a.mix[i] = stripMix{
			faderDB: a.law.DBFromPosition(a.floatAt(ad.fader)),
			audible: a.intAt(ad.on) == 1 && uint8(a.intAt(ad.groups))&engaged == 0 && uint8(a.intAt(ad.dcas))&dcaMuted == 0,
			pan:     pan,
		}
	}
}

func (a *animator) mainDB() float64 {
	if a.intAt(a.fam.mainOn) != 1 {
		return math.Inf(-1)
	}
	return a.law.DBFromPosition(a.floatAt(a.fam.mainFader))
}

func (a *animator) floatAt(addr string) float64 {
	v, _ := a.sim.Value(addr)
	return float64(v.Float)
}

func (a *animator) intAt(addr string) int32 {
	v, _ := a.sim.Value(addr)
	return v.Int
}

// deskChange moves the next fader of the script as an engineer at the desk
// would: the change is pushed to every subscribed client (origin console).
func (a *animator) deskChange() {
	m := deskScript[a.deskNext%len(deskScript)]
	a.deskNext++
	if m.ch > a.fam.inputs {
		return
	}
	addr := a.addrs[m.ch-1].fader
	db := a.law.DBFromPosition(a.floatAt(addr))
	if math.IsInf(db, -1) {
		return
	}
	pos := a.law.PositionFromDB(db + m.deltaDB)
	if err := a.sim.SurfaceSet(addr, osc.Float(float32(pos))); err != nil {
		a.log.Debug("demo: desk change refused", "addr", addr, "err", err)
	}
}

// mainMix is the post-fader stereo sum of the audible inputs, scaled by the
// main fader, in dBFS (power sum; pan as a balance law).
func mainMix(levels []float64, mix []stripMix, mainDB float64) (l, r float64) {
	var pl, pr float64
	for i, m := range mix {
		if !m.audible {
			continue
		}
		amp := dbToAmp(levels[i] + m.faderDB)
		gl, gr := min(1, 1-m.pan), min(1, 1+m.pan)
		pl += amp * amp * gl * gl
		pr += amp * amp * gr * gr
	}
	g := dbToAmp(mainDB)
	return ampToDB(math.Sqrt(pl) * g), ampToDB(math.Sqrt(pr) * g)
}

// ampToDB is dBFS of a linear amplitude, at most mainCeilingDB; 0 is -inf.
func ampToDB(amp float64) float64 {
	if amp <= 0 {
		return math.Inf(-1)
	}
	return min(20*math.Log10(amp), mainCeilingDB)
}
