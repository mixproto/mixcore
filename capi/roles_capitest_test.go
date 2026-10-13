// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build capitest

package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// Core-api v0.4 through the C ABI: a monitor seat's write outside its mix
// is MIX_ERR_ROLE (11) and reaches nothing; its own send is MIX_OK.
func TestABI_MonitorRoleAndSends(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{Name: "ROLE"})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	defer func() { cancel(); wg.Wait(); _ = pc.Close() }()
	testSetListeners(stateCtx, discoveryCtx, logCtx)
	defer func() { MixSetStateListener(nil, 0); MixSetDiscoveryListener(nil, 0); MixSetLogSink(nil, 0, 0) }()
	cfg := fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d,"role":{"kind":"monitor","buses":[{"bus":2}]}}`,
		pc.LocalAddr().(*net.UDPAddr).Port)
	if _, code := testStart(cfg); code != 0 {
		t.Fatalf("MixStart = %d", code)
	}
	defer MixStop()
	waitFor(t, "this session's live snapshot with sends", func() bool { // the log keeps earlier tests' messages
		for _, m := range testMessages(stateCtx) {
			if strings.Contains(m, `"name":"ROLE"`) && strings.Contains(m, `"status":"live"`) && strings.Contains(m, `"sendFields":{"on":true`) {
				return true
			}
		}
		return false
	})
	other := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":0,"origin":{"kind":"gesture","clientId":"m","seq":1}}`
	if code := testApply(other); code != 11 {
		t.Fatalf("channel fader from a monitor seat = %d, want MIX_ERR_ROLE", code)
	}
	own := `{"version":0,"type":"toggle","param":{"strip":{"kind":"input","index":3},"field":"sendOn","bus":2},"origin":{"kind":"gesture","clientId":"m","seq":2}}`
	if code := testApply(own); code != 0 {
		t.Fatalf("own send on = %d", code)
	}
	waitFor(t, "the write", func() bool { return sim.WriteCount() == 1 })
	if ws := sim.Writes(); len(ws) != 1 || ws[0].Address != "/ch/03/mix/02/on" {
		t.Fatalf("writes %+v", ws)
	}
}
