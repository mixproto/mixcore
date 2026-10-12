// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

func phantom(i int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripHeadamp, Index: i}, Field: model.FieldPhantom}
}

// AC-PS-20 on the line law (XA-F112, aux 17): the codec reproduces the
// float32 of xair.md's line table and reads it back; the simulator's value
// set at the surface arrives as the same dB.
func TestAux_LineLawRoundTrip(t *testing.T) {
	c, ok := New(Config{}).Codec(headamp(auxHeadamp))
	if !ok {
		t.Fatal("no aux headamp")
	}
	for _, pt := range []struct {
		db  float64
		hex uint32
	}{{-12, 0x00000000}, {0, 0x3ec00000}, {19.5, 0x3f7c0000}, {20, 0x3f800000}} {
		a, err := c.ToWire(level(t, pt.db))
		if err != nil || math.Float32bits(a.Float) != pt.hex {
			t.Errorf("%v dB -> %08x, %v (want %08x)", pt.db, math.Float32bits(a.Float), err, pt.hex)
		}
		v, err := c.FromWire(osc.Float(math.Float32frombits(pt.hex)))
		if db, _ := v.DB(); err != nil || db != pt.db {
			t.Errorf("%08x -> %v, %v", pt.hex, v, err)
		}
	}
	if _, err := c.ToWire(level(t, 20.5)); !errors.Is(err, model.ErrInvalidValue) {
		t.Fatalf("+20.5 dB is above the line law: %v", err)
	}
}

// XA-F112: the line law is unconfirmed, so aux Gain is shown and never
// written; phantom is never written either (a future show-mode gate).
func TestHeadamp_AuxGainAndPhantomAreReadOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		for _, cmd := range []driver.Command{
			{Param: headamp(auxHeadamp), Value: level(t, 0), Origin: gesture},
			{Param: phantom(1), Value: model.Bool(true), Origin: gesture},
		} {
			if err := sess.Apply(cmd); !errors.Is(err, driver.ErrUnsupported) {
				t.Errorf("%s: %v", cmd.Param, err)
			}
		}
		if n := r.sim.WriteCount(); n != 0 {
			t.Fatalf("%d writes reached the mixer", n)
		}
	})
}

// XA-F113: phantom changed at the mixer reaches the model as a console change.
func TestHeadamp_PhantomPushed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{})
		sess := r.connect()
		events, _ := r.run(sess)
		if err := r.sim.SurfaceSet("/headamp/02/phantom", osc.Int(1)); err != nil {
			t.Fatal(err)
		}
		ev, ok := nextOf[driver.ParamChanged](events, time.Second)
		if !ok || ev.Param != phantom(2) || !ev.Value.Equal(model.Bool(true)) || ev.Origin.Kind != driver.OriginConsole {
			t.Fatalf("phantom push: %+v %v", ev, ok)
		}
	})
}

// XA-F110: aux 17 exists on the 18-channel models only. A mixer with
// fewer mic headamps never gets a read of /headamp/17 (a line input there,
// numbering unverified): it stays absent, not guessed.
func TestHeadamp_SmallModelHasNoAuxAndNoAuxRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{MicHeadamps: 8})
		sess := r.connect()
		if _, ok := sess.Capabilities().Headamp(auxHeadamp); ok {
			t.Fatal("aux described on an 8-headamp mixer")
		}
		for _, e := range r.sim.Log() {
			if e.Address == "/headamp/17/gain" {
				t.Fatalf("aux read sent to a small model at %v", e.At)
			}
		}
	})
}

// The 6 dB bound and the +3 / -6 dB caps are in dB (console rules); on
// both laws they are 12 and 6 / 12 wire steps (x32.md after X32-F79).
func TestHeadamp_DBRulesAreTheSameStepsOnBothLaws(t *testing.T) {
	d := New(Config{})
	for _, h := range []int{1, auxHeadamp} {
		c, _ := d.Codec(headamp(h))
		step := func(db float64) int {
			a, err := c.ToWire(level(t, db))
			if err != nil {
				t.Fatal(err)
			}
			steps := lawOf(h)
			return int(math.Round(float64(a.Float) * float64(steps)))
		}
		base := step(0)
		if up6, up3, dn6 := step(6)-base, step(3)-base, base-step(-6); up6 != 12 || up3 != 6 || dn6 != 12 {
			t.Errorf("headamp %d: +6 = %d, +3 = %d, -6 = %d steps", h, up6, up3, dn6)
		}
	}
}

func lawOf(h int) (steps int) {
	if h == auxHeadamp {
		return auxGainSteps
	}
	return micGainSteps
}
