// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"testing"

	"go.uber.org/goleak"

	simx32 "github.com/mixproto/mixcore/sim/x32"
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
