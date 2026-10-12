// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// Console families the demo can be (contracts/core-api demo.schema.json).
const (
	FamilyX32  = "x32"
	FamilyXAir = "xair"
)

// consoleName is what the simulated console reports in /xinfo; UIs show
// their own localized Demo label instead.
const consoleName = "Demo"

// Wire facts used by the seed (docs/protocols/x32.md, xair.md).
const (
	x32Inputs     = 32 // X32-F30
	x32MuteGroups = 6  // X32-F35
	x32DCAs       = 8  // X32-F37
	xairInputs    = 16 // XA-F71
	xairGroups    = 4  // xair.md section 3.1: 4 mute groups, 4 DCAs
	// xairBank1Values: /meters/1 holds 40 values (XA-F53).
	xairBank1Values = 40
	xairTapPre      = 3 // xair.md section 3.3: tap 0..5 = IN, PREEQ, POSTEQ, PRE, POST, GRP
	// Mic headamp Gain: -12 + 72 f dB (X32-F71, XA-F111).
	micGainMinDB  = -12.0
	micGainSpanDB = 72.0
	noHeadampHA   = -1 // X32-F74: /-ha/NN/index of a source without a headamp
	// xairDBSteps: meter values are int16 in 1/256 dB (XA-F52).
	xairDBSteps = 256
)

// console is what the demo needs of a family simulator.
type console interface {
	Serve(ctx context.Context, pc net.PacketConn) error
	SurfaceSet(addr string, a osc.Arg) error
	Value(addr string) (osc.Arg, bool)
}

// meterOut publishes one tick of input (pre-fader) and Main L/R levels in dBFS.
type meterOut interface {
	publish(inputs []float64, mainL, mainR float64)
}

// family is the shape of one simulated console.
type family struct {
	inputs, muteGroups, dcas int
	mainFader, mainOn        string
	hasPan                   bool // <strip>/mix/pan is modelled (xair)
	// headamp is the root of input ch's mic headamp (default patch: ch N, headamp N).
	headamp  func(ch int) string
	hasPatch bool // /-ha/NN/index names each channel's headamp (x32)
}

var families = map[string]family{
	// X32-F33, X32-F34: /main/st is the main bus.
	// X32-F70: /headamp/000..127, three digits, 0-based; local 000..031.
	FamilyX32: {inputs: x32Inputs, muteGroups: x32MuteGroups, dcas: x32DCAs, mainFader: "/main/st/mix/fader", mainOn: "/main/st/mix/on",
		headamp: func(ch int) string { return fmt.Sprintf("/headamp/%03d", ch-1) }, hasPatch: true},
	// xair.md section 3.1: /lr is the main bus; pan per xair.md section 3.2.
	// XA-F110: mic headamps /headamp/01..16, two digits, 1-based.
	FamilyXAir: {inputs: xairInputs, muteGroups: xairGroups, dcas: xairGroups, mainFader: "/lr/mix/fader", mainOn: "/lr/mix/on", hasPan: true,
		headamp: func(ch int) string { return fmt.Sprintf("/headamp/%02d", ch) }},
}

// newConsole builds the family's simulator, seeded with the show.
func newConsole(name string, law driver.FaderLaw, log *slog.Logger) (console, meterOut, error) {
	fam := families[name]
	if name == FamilyXAir {
		sim := simxair.New(simxair.Options{Name: consoleName, Logger: log})
		s := seeder{sim: sim, law: law}
		s.common(fam)
		s.xairExtras()
		return sim, &xairMeters{sim: sim}, s.err
	}
	sim := simx32.New(simx32.Options{Name: consoleName, Logger: log})
	s := seeder{sim: sim, law: law}
	s.common(fam)
	return sim, &x32Meters{sim: sim}, s.err
}

// seeder sets values as the console itself would (no client write is
// logged) and keeps the first error.
type seeder struct {
	sim console
	law driver.FaderLaw
	err error
}

func (s *seeder) set(addr string, a osc.Arg) {
	if s.err == nil {
		if err := s.sim.SurfaceSet(addr, a); err != nil {
			s.err = fmt.Errorf("demo: seed %s: %w", addr, err)
		}
	}
}

func (s *seeder) level(db float64) osc.Arg { return osc.Float(float32(s.law.PositionFromDB(db))) }

// common seeds what both families model: names (X32-F31), faders (X32-F32,
// XA-F40), mutes (X32-F33: 0 = muted), membership (X32-F36, X32-F38) and
// engaged mute groups (X32-F35: 1 = engaged); xair uses the same addresses
// (xair.md section 3.2).
func (s *seeder) common(fam family) {
	for i := range fam.inputs {
		c, root := channels[i], chRoot(i+1)
		s.set(root+"/config/name", osc.String(c.name))
		s.set(root+"/mix/fader", s.level(c.faderDB))
		s.set(root+"/mix/on", osc.Int(boolInt(!c.muted)))
		s.set(root+"/grp/mute", osc.Int(int32(c.groups)))
		s.set(root+"/grp/dca", osc.Int(int32(c.dcas)))
	}
	s.set(fam.mainFader, s.level(mainFaderDB))
	for g := 1; g <= fam.muteGroups; g++ {
		s.set(fmt.Sprintf("/config/mute/%d", g), osc.Int(boolInt(engagedMuteGroups&(1<<(g-1)) != 0)))
	}
	s.headamps(fam)
}

// headamps seeds every input's mic headamp: Gain (X32-F71, X32-F72;
// XA-F111) and 48 V (X32-F73, XA-F113, 1 = on). On x32 a channel whose
// source has no headamp is patched to none (X32-F74).
func (s *seeder) headamps(fam family) {
	for i := range fam.inputs {
		p, root := preamps[i], fam.headamp(i+1)
		s.set(root+"/gain", osc.Float(float32((p.gainDB-micGainMinDB)/micGainSpanDB)))
		s.set(root+"/phantom", osc.Int(boolInt(p.phantom)))
		if fam.hasPatch && p.noHeadamp {
			s.set(fmt.Sprintf("/-ha/%02d/index", i), osc.Int(noHeadampHA))
		}
	}
}

// xairExtras seeds what only the xair simulator models (xair.md section 3):
// colours, pans, DCA names and faders, monitor bus names, faders and pre-fader sends.
func (s *seeder) xairExtras() {
	for i := range xairInputs {
		c, root := channels[i], chRoot(i+1)
		s.set(root+"/config/color", osc.Int(c.colour))
		s.set(root+"/mix/pan", osc.Float(float32((c.pan+1)/2))) // 0..1 = L100..R100
	}
	for d, name := range dcaNames {
		s.set(fmt.Sprintf("/dca/%d/config/name", d+1), osc.String(name))
		s.set(fmt.Sprintf("/dca/%d/fader", d+1), s.level(dcaFaderDB))
	}
	for b, bus := range monitorBuses {
		s.set(fmt.Sprintf("/bus/%d/config/name", b+1), osc.String(bus.name))
		s.set(fmt.Sprintf("/bus/%d/mix/fader", b+1), s.level(busFaderDB))
		for ch := 1; ch <= xairInputs; ch++ {
			send := fmt.Sprintf("%s/mix/%02d", chRoot(ch), b+1) // two digits (xair.md section 3.3)
			s.set(send+"/level", s.level(sendDB(bus, ch)))
			s.set(send+"/tap", osc.Int(xairTapPre))
		}
	}
}

func chRoot(ch int) string { return fmt.Sprintf("/ch/%02d", ch) }

func boolInt(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// x32Meters writes linear levels, 1.0 = 0 dBFS (X32-F54): inputs to bank
// /meters/1, Main L/R to bank /meters/2 (X32-F55).
type x32Meters struct {
	sim *simx32.Sim
	lin [x32Inputs]float32
}

func (m *x32Meters) publish(inputs []float64, mainL, mainR float64) {
	for i, db := range inputs[:min(len(inputs), x32Inputs)] {
		m.lin[i] = float32(dbToAmp(db))
	}
	m.sim.SetInputLevels(m.lin[:])
	m.sim.SetMainLevels(float32(dbToAmp(mainL)), float32(dbToAmp(mainR)))
}

// xairMeters writes bank /meters/1 in 1/256 dB (XA-F52): inputs at 0..15,
// Main L/R at 36, 37, everything else silent (XA-F53).
type xairMeters struct {
	sim  *simxair.Sim
	bank [xairBank1Values]int16
}

func (m *xairMeters) publish(inputs []float64, mainL, mainR float64) {
	for i := range m.bank {
		m.bank[i] = simxair.MeterSilence
	}
	for i, db := range inputs[:min(len(inputs), xairInputs)] {
		m.bank[i] = xairRaw(db)
	}
	m.bank[simxair.Bank1MainL], m.bank[simxair.Bank1MainL+1] = xairRaw(mainL), xairRaw(mainR)
	m.sim.SetMeterValues(m.bank[:])
}

// xairRaw encodes dBFS as int16 1/256 dB; -inf and anything at or below
// -128 dB is silence (XA-F52), and levels never exceed 0 dBFS here.
func xairRaw(db float64) int16 {
	raw := math.Round(min(db, 0) * xairDBSteps)
	if math.IsNaN(raw) || raw <= math.MinInt16 {
		return simxair.MeterSilence
	}
	return int16(raw)
}

// dbToAmp is a linear amplitude; -inf is 0.
func dbToAmp(db float64) float64 {
	if math.IsInf(db, -1) || math.IsNaN(db) {
		return 0
	}
	return math.Pow(10, db/20)
}
