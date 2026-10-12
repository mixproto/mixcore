// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"math"
)

// Meter display scale and ballistics shared by every UI (contracts/core-api
// meter-frame.md, design CR-17, open question 24). Not protocol facts: both
// X32-family protocols send raw levels only.
const (
	MeterMinDB          = -60.0
	MeterMaxDB          = 0.0
	MeterClipDB         = 0.0
	MeterNominalDB      = -18.0
	MeterReleaseDBPerS  = 20.0
	MeterPeakHoldMs     = 1500
	msPerSecond         = 1000.0
	stepBottomDB        = -60.0 // CR-21: -inf <-> -60 dB
	stepResolutionPerDB = 10.0  // levels are shown and stepped in 0.1 dB
)

// meterTicksDB are the scale's tick marks, top to bottom.
var meterTicksDB = []float64{0, -6, -12, -18, -30, -60}

type meterScaleJSON struct {
	Version            int       `json:"version"`
	MinDB              float64   `json:"minDB"`
	MaxDB              float64   `json:"maxDB"`
	TicksDB            []float64 `json:"ticksDB"`
	ClipDB             float64   `json:"clipDB"`
	NominalDB          float64   `json:"nominalDB"`
	ReleaseDBPerSecond float64   `json:"releaseDBPerSecond"`
	PeakHoldMs         int       `json:"peakHoldMs"`
}

// meterScale is encoded once; it never changes.
var meterScale = func() string {
	b, err := json.Marshal(meterScaleJSON{
		Version: contractVersion, MinDB: MeterMinDB, MaxDB: MeterMaxDB, TicksDB: meterTicksDB, ClipDB: MeterClipDB,
		NominalDB: MeterNominalDB, ReleaseDBPerSecond: MeterReleaseDBPerS, PeakHoldMs: MeterPeakHoldMs,
	})
	if err != nil {
		panic(err) // constant input: cannot fail
	}
	return string(b)
}()

// MeterScale returns the meter display scale (meter-scale.schema.json). Never blocks.
func (e *Engine) MeterScale() string { return meterScale }

// MeterPosition maps a level in dBFS to 0..1 on the meter scale. Never blocks.
func (e *Engine) MeterPosition(dbfs float64) float64 {
	if math.IsNaN(dbfs) || dbfs <= MeterMinDB {
		return 0
	}
	if dbfs >= MeterMaxDB {
		return 1
	}
	return (dbfs - MeterMinDB) / (MeterMaxDB - MeterMinDB)
}

// MeterRelease is one ballistics step: inputDB above shownDB is shown at
// once; otherwise the shown level falls by MeterReleaseDBPerS over elapsedMs,
// never below inputDB, and a fall below the scale gives -inf. NaN counts as
// silence. Never blocks, never allocates.
func (e *Engine) MeterRelease(shownDB, inputDB, elapsedMs float64) float64 {
	shown, in := silenceIfNaN(shownDB), silenceIfNaN(inputDB)
	if in >= shown {
		return in
	}
	fallen := shown - MeterReleaseDBPerS*max(elapsedMs, 0)/msPerSecond
	if math.IsNaN(fallen) {
		fallen = shown // elapsedMs was NaN
	}
	out := max(fallen, in)
	if out < MeterMinDB {
		return math.Inf(-1)
	}
	return out
}

func silenceIfNaN(db float64) float64 {
	if math.IsNaN(db) {
		return math.Inf(-1)
	}
	return db
}

// FaderStep applies a discrete step (keyboard, screen reader, +/- buttons)
// with the bottom-of-scale rule of design CR-21: up from -inf goes to -60 dB,
// down from -60 dB or below goes to -inf; otherwise the level rounded to 0.1 dB
// plus the step, rounded to 0.1 dB, capped at the top of the family's fader
// law. A zero or NaN step returns db unchanged. Never blocks.
func (e *Engine) FaderStep(db, deltaDB float64) float64 {
	if deltaDB == 0 || math.IsNaN(deltaDB) {
		return db
	}
	up := deltaDB > 0
	if math.IsNaN(db) || math.IsInf(db, -1) {
		if up {
			return stepBottomDB
		}
		return math.Inf(-1)
	}
	base := roundTenth(db)
	if !up && base <= stepBottomDB {
		return math.Inf(-1)
	}
	top := e.FaderDB(1)
	if math.IsInf(top, -1) { // no family law loaded
		top = math.Inf(1)
	}
	return min(roundTenth(base+deltaDB), top)
}

func roundTenth(db float64) float64 {
	v := math.Round(db*stepResolutionPerDB) / stepResolutionPerDB
	if v == 0 {
		return 0 // never -0
	}
	return v
}
