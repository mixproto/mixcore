// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

func headamp(i int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: i}, Field: model.FieldGain}
}

// AC-PS-20, CR-23: Gain round trip dB -> wire -> mixer -> dB at both range
// ends and around unity, through the real write path and the simulator.
// The mic law has no breaks (XA section 4: dB = -12 + 72 f).
func TestGain_RoundTripAtBothRangeEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		for i, db := range []float64{-12, -11.5, 0, 32.5, 59.5, 60} {
			h := i + 1
			if err := sess.Apply(driver.Command{Param: headamp(h), Value: level(t, db), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			ev, ok := nextOf[driver.ParamChanged](events, time.Second)
			got, _ := ev.Value.DB()
			wire, _ := r.sim.Value(headampAddr(h))
			want := float32((db + 12) / 0.5 / 144)
			if !ok || ev.Param != headamp(h) || got != db || wire.Float != want {
				t.Errorf("%v dB: wire %v (want %v), echo %v", db, wire.Float, want, got)
			}
		}
		r.assertWritesAudited()
	})
}

func headampAddr(h int) string { return fmt.Sprintf("/headamp/%02d/gain", h) }

// AC-PS-20: no in-range value encodes to the wire maximum except the range
// maximum; values outside the range are errors, never clamped.
func TestGain_WireMaximumOnlyForTheMaximum(t *testing.T) {
	c, ok := New(Config{}).Codec(headamp(1))
	if !ok {
		t.Fatal("no headamp 1")
	}
	toWire := func(db float64) (float32, error) {
		v := model.Value{}
		if !math.IsNaN(db) && !math.IsInf(db, 1) {
			v, _ = model.Level(db)
		}
		a, err := c.ToWire(v)
		return a.Float, err
	}
	for db := -12.0; db < 60; db += 0.01 {
		f, err := toWire(db)
		if err != nil || f >= 1 {
			t.Fatalf("%v dB -> %v, %v", db, f, err)
		}
	}
	if f, err := toWire(60); err != nil || f != 1 {
		t.Fatalf("60 dB -> %v, %v", f, err)
	}
	for _, bad := range []float64{-12.01, 60.01, math.Inf(1), math.Inf(-1), math.NaN()} {
		if _, err := toWire(bad); !errors.Is(err, model.ErrInvalidValue) {
			t.Errorf("%v dB accepted: %v", bad, err)
		}
	}
	for _, k := range []int{0, 1, 143, 144} {
		v, err := c.FromWire(osc.Float(float32(float64(k) / 144)))
		if db, _ := v.DB(); err != nil || db != -12+0.5*float64(k) {
			t.Errorf("step %d -> %v, %v", k, v, err)
		}
	}
	for _, bad := range []float32{-0.01, 1.01, float32(math.NaN())} {
		if _, err := c.FromWire(osc.Float(bad)); err == nil {
			t.Errorf("wire %v accepted", bad)
		}
	}
}

// XA-F73: an XR16 answers 8 mic headamps, an XR12 4; Gain is absent above.
func TestGain_SmallModelsHaveFewerHeadamps(t *testing.T) {
	for _, n := range []int{4, 8} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, simxair.Options{MicHeadamps: n})
			sess := r.connect()
			if c := sess.Capabilities(); c.Headamps != n || len(sess.Snapshot().Headamps()) != n {
				t.Fatalf("%d headamps: caps %+v", n, c)
			}
			err := sess.Apply(driver.Command{Param: headamp(n + 1), Value: level(t, 0), Origin: gesture})
			if !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("headamp %d: %v", n+1, err)
			}
		})
	}
}

// The snapshot carries the mixer's Gain values.
func TestGain_ReadAtConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		if err := r.sim.SurfaceSet("/headamp/03/gain", osc.Float(89.0/144)); err != nil { // +32.5 dB
			t.Fatal(err)
		}
		hs := r.connect().Snapshot().Headamps()
		if len(hs) != 16 || hs[2].GainDB != 32.5 || hs[0].GainDB != 0 {
			t.Fatalf("headamps %+v", hs)
		}
	})
}
