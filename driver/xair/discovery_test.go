// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	simx32 "github.com/mixproto/mixcore/sim/x32"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// XA-F12..F14: a broadcast on 10024 finds X Air mixers; a probe that also
// targets an X32 port gets that console's reply from 10023, and the reply
// port and model tell the families apart. A probe never writes.
func TestProbe_FindsMixersAndTellsFamiliesApart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{Name: "XR18-1D-DA-B4", Model: "XR18V2"})
		x32sim := simx32.New(simx32.Options{Name: "FOH"})
		pc, _ := r.net.ListenPacket("10.0.9.9:10023")
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { _ = x32sim.Serve(ctx, pc) })
		defer func() { cancel(); wg.Wait(); _ = pc.Close() }()

		targets := append(BroadcastTargets(), netip.MustParseAddrPort("10.0.9.9:10023"), netip.MustParseAddrPort("10.0.9.9:10024"))
		found, err := Probe(context.Background(), r.net.Host(clientIP), targets, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 2 {
			t.Fatalf("found %+v, want the mixer and the X32 once each", found)
		}
		for _, f := range found {
			isAir := f.Endpoint.Port == DefaultPort && IsModel(f.Identity.Model)
			if (f.Identity.Name == "XR18-1D-DA-B4") != isAir {
				t.Fatalf("%+v classified as xair = %t", f, isAir)
			}
		}
		if LimitedBroadcast.Port() != DefaultPort || len(r.sim.Writes())+len(x32sim.Writes()) != 0 {
			t.Fatal("broadcast port, or a probe wrote")
		}
	})
}
