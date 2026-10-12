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
		// The lead's AC-PS-20 points, with the float32 of x32.md's reference
		// table (the mic law is the X32 law, XA-F111).
		for i, pt := range []struct {
			db  float64
			hex uint32
		}{{-12, 0x00000000}, {0, 0x3e2aaaab}, {0.5, 0x3e31c71c}, {24, 0x3f000000}, {59.5, 0x3f7e38e4}, {60, 0x3f800000}} {
			h := i + 1
			if err := sess.Apply(driver.Command{Param: headamp(h), Value: level(t, pt.db), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			ev, ok := nextOf[driver.ParamChanged](events, time.Second)
			got, _ := ev.Value.DB()
			wire, _ := r.sim.Value(headampAddr(h))
			if !ok || ev.Param != headamp(h) || got != pt.db || math.Float32bits(wire.Float) != pt.hex || ev.Origin != gesture {
				t.Errorf("%v dB: wire %08x (want %08x), echo %v %v", pt.db, math.Float32bits(wire.Float), pt.hex, got, ev.Origin)
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
			if c := sess.Capabilities(); len(c.Headamps) != n || len(sess.Snapshot().Headamps()) != n {
				t.Fatalf("%d headamps: caps %+v", n, c)
			}
			err := sess.Apply(driver.Command{Param: headamp(n + 1), Value: level(t, 0), Origin: gesture})
			if !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("headamp %d: %v", n+1, err)
			}
		})
	}
}

// The snapshot carries the mixer's Gain and phantom values: 16 mic
// headamps with phantom, and aux 17 on its own line law (XA-F112) without.
func TestGain_ReadAtConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		for addr, v := range map[string]osc.Arg{
			"/headamp/03/gain": osc.Float(89.0 / 144), "/headamp/03/phantom": osc.Int(1), // +32.5 dB, 48 V on
			"/headamp/17/gain": osc.Float(63.0 / 64), // +19.5 dB on the line law
		} {
			if err := r.sim.SurfaceSet(addr, v); err != nil {
				t.Fatal(err)
			}
		}
		hs := r.connect().Snapshot().Headamps()
		want3 := model.Headamp{Index: 3, GainDB: 32.5, Phantom: true, HasPhantom: true}
		if len(hs) != 17 || hs[2] != want3 || hs[0].GainDB != 0 || hs[16] != (model.Headamp{Index: 17, GainDB: 19.5}) {
			t.Fatalf("headamps %+v", hs)
		}
	})
}
