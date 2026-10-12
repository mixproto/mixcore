// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

var negInf = math.Inf(-1)

func TestMeterScale_MatchesTheContractExample(t *testing.T) {
	e := NewEngine()
	defer e.Close()
	var got, want map[string]any
	if err := json.Unmarshal([]byte(e.MeterScale()), &got); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../contracts/core-api/meter-scale.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct{ Examples []map[string]any }
	if err := json.Unmarshal(b, &schema); err != nil || len(schema.Examples) != 1 {
		t.Fatalf("schema examples: %v", err)
	}
	want = schema.Examples[0]
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("MeterScale = %s, contract example %s", gb, wb)
	}
}

func TestMeterPosition(t *testing.T) {
	e := NewEngine()
	defer e.Close()
	for _, tc := range []struct{ db, pos float64 }{
		{negInf, 0}, {math.NaN(), 0}, {-90, 0}, {-60, 0}, {-30, 0.5}, {-18, 0.7}, {0, 1}, {6, 1}, {math.Inf(1), 1},
	} {
		if got := e.MeterPosition(tc.db); math.Abs(got-tc.pos) > 1e-9 {
			t.Errorf("MeterPosition(%v) = %v, want %v", tc.db, got, tc.pos)
		}
	}
}

func TestMeterRelease_Ballistics(t *testing.T) {
	e := NewEngine()
	defer e.Close()
	for _, tc := range []struct {
		name                  string
		shown, input, elapsed float64
		want                  float64
	}{
		{"attack is instant", -30, -10, 16, -10},
		{"falls 20 dB/s", -10, -40, 100, -12},
		{"never below the input", -10, -10.5, 100, -10.5},
		{"silence falls too", -10, negInf, 500, -20},
		{"below the scale is off", -59, negInf, 100, negInf},
		{"off stays off", negInf, negInf, 16, negInf},
		{"NaN input is silence", -10, math.NaN(), 100, -12},
		{"NaN shown is off", math.NaN(), -20, 16, -20},
		{"no time, no fall", -10, -40, 0, -10},
		{"negative time is no time", -10, -40, -5, -10},
	} {
		if got := e.MeterRelease(tc.shown, tc.input, tc.elapsed); !sameDB(got, tc.want) {
			t.Errorf("%s: MeterRelease(%v, %v, %v) = %v, want %v", tc.name, tc.shown, tc.input, tc.elapsed, got, tc.want)
		}
	}
}

// CR-21: up from -inf is -60 dB, down from -60 dB or below is -inf; else 0.1 dB
// rounding and the top of the x32 law (+10 dB).
func TestFaderStep_BottomOfScaleRule(t *testing.T) {
	e := NewEngine()
	defer e.Close()
	for _, tc := range []struct {
		db, delta, want float64
	}{
		{negInf, 1, -60}, {negInf, 0.1, -60}, {negInf, -1, negInf},
		{-60, -1, negInf}, {-60.04, -0.1, negInf}, {-75, -5, negInf}, {-75, 1, -74},
		{-59.5, -1, -60.5}, {-60, 1, -59}, {0, 1, 1}, {-3.26, 0.1, -3.2}, {-0.05, 0.1, 0},
		{9.5, 1, 10}, {10, 5, 10}, {-10, 0, -10}, {-10, math.NaN(), -10}, {math.NaN(), 1, -60},
	} {
		if got := e.FaderStep(tc.db, tc.delta); !sameDB(got, tc.want) {
			t.Errorf("FaderStep(%v, %v) = %v, want %v", tc.db, tc.delta, got, tc.want)
		}
	}
}

func sameDB(a, b float64) bool {
	if math.IsInf(a, -1) || math.IsInf(b, -1) {
		return math.IsInf(a, -1) && math.IsInf(b, -1)
	}
	return math.Abs(a-b) < 1e-9
}
