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
	"time"

	simx32 "github.com/mixproto/mixcore/sim/x32"
)

const (
	stateCtx     = 1
	discoveryCtx = 2
	logCtx       = 3
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func anyContains(ctx int, sub string) bool {
	for _, m := range testMessages(ctx) {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// The whole C ABI with real C memory and C function pointers against a
// simulator on loopback: start, state callback, gesture, meters, discovery, logs.
func TestABI_StartApplyMetersDiscoverWithCMemory(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{Name: "ABI"})
	sim.SetInputLevels([]float32{1})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	defer func() { cancel(); wg.Wait(); _ = pc.Close() }()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	testSetListeners(stateCtx, discoveryCtx, logCtx)
	defer func() { MixSetStateListener(nil, 0); MixSetDiscoveryListener(nil, 0); MixSetLogSink(nil, 0, 0) }()
	session, code := testStart(fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d,"meters":{"inputs":true}}`, port))
	if code != 0 || !strings.Contains(session, fmt.Sprintf(`"port":%d`, port)) {
		t.Fatalf("MixStart = %d, %q", code, session)
	}
	defer MixStop()
	if _, code := testStart(`{"version":0,"family":"x32","host":"127.0.0.1"}`); code != 9 {
		t.Fatalf("second MixStart = %d, want MIX_ERR_ALREADY_STARTED", code)
	}
	waitFor(t, "live state", func() bool { return anyContains(stateCtx, `"status":"live"`) })
	waitFor(t, "a log line", func() bool { return anyContains(logCtx, "console: live") })

	cmd := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":-10,"origin":{"kind":"gesture","clientId":"abi","seq":1}}`
	if code := testApply(cmd); code != 0 {
		t.Fatalf("MixApply = %d", code)
	}
	waitFor(t, "the echo", func() bool { return anyContains(stateCtx, `"clientId":"abi"`) })
	if code := testApply(strings.Replace(cmd, "gesture", "system", 1)); code != 4 {
		t.Fatalf("MixApply with a system origin = %d, want MIX_ERR_NOT_USER_ORIGIN", code)
	}
	if res, code := testGesture(strings.Replace(cmd, "-10", "-12", 1)); code != 0 || !strings.Contains(res, `"state":"free"`) {
		t.Fatalf("MixGesture = %d, %q", code, res)
	}
	typed := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader"},"value":0,"typed":true,"origin":{"kind":"gesture","clientId":"abi","seq":2}}`
	if res, code := testGesture(typed); code != 15 || res != "" {
		t.Fatalf("typed raise without confirmed = %d %q, want MIX_ERR_NEEDS_CONFIRM", code, res)
	}

	waitFor(t, "a meter frame", func() bool { b, _ := testMeterFrame(536); return len(b) == 24+34*4 })
	if _, n := testMeterFrame(10); n != 24+34*4 {
		t.Fatalf("short buffer returned %d, want the needed length", n)
	}

	req := fmt.Sprintf(`{"version":0,"family":"x32","hosts":[{"host":"127.0.0.1","port":%d}],"waitMs":200}`, port)
	if code := testDiscover(req); code != 0 {
		t.Fatalf("MixDiscover = %d", code)
	}
	waitFor(t, "a discovery result", func() bool { return anyContains(discoveryCtx, `"name":"ABI"`) })
	if code := MixPause(); code != 0 {
		t.Fatalf("MixPause = %d", code)
	}
	if code := MixResume(); code != 0 {
		t.Fatalf("MixResume = %d", code)
	}
	if code := MixRequestSnapshot(); code != 0 {
		t.Fatalf("MixRequestSnapshot = %d", code)
	}
	if n := len(sim.Writes()); n != 2 {
		t.Fatalf("console received %d writes, want exactly the two gestures (Apply, Gesture)", n)
	}
}
