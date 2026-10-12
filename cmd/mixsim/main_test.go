// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"go.uber.org/goleak"

	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestDemoSetup(t *testing.T) {
	sim := simx32.New(simx32.Options{})
	if err := fillDemoNames(sim); err != nil {
		t.Fatal(err)
	}
	if v, _ := sim.Value("/ch/32/config/name"); string(v.Bytes) != "Input 32" {
		t.Fatalf("ch32 name = %q", v.Bytes)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*demoTick)
	defer cancel()
	animateMeters(ctx, sim) // returns when ctx ends
	if len(sim.Writes()) != 0 {
		t.Fatal("demo setup is surface activity, not client writes")
	}
}

func TestDemoSetup_XAir(t *testing.T) {
	sim := simxair.New(simxair.Options{})
	if err := fillXAirDemo(sim); err != nil {
		t.Fatal(err)
	}
	if v, _ := sim.Value("/ch/16/config/name"); string(v.Bytes) != "Input 16" {
		t.Fatalf("ch16 name = %q", v.Bytes)
	}
	if v, _ := sim.Value("/lr/mix/fader"); v.Float != 767.0/1023 {
		t.Fatalf("main fader %v", v.Float)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*demoTick)
	defer cancel()
	animateXAir(ctx, sim)
	if sim.WriteCount() != 0 {
		t.Fatal("demo setup is not client writes")
	}
}

func TestNewServer_Families(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, fam := range []string{familyX32, familyXAir} {
		s, err := newServer(context.Background(), flags{family: fam}, log)
		if err != nil || s == nil {
			t.Fatalf("%s: %v", fam, err)
		}
	}
}
