// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build capitest

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	demoStateCtx     = 11
	demoDiscoveryCtx = 12
	demoLogCtx       = 13
)

// The demo button through the C ABI: MixStartDemo, MixStart on its address,
// a gesture and its echo, meters, MixStopDemo, with real C memory.
func TestABI_DemoMixer(t *testing.T) {
	if code := MixStartDemo(nil, nil, nil); code != 14 {
		t.Fatalf("MixStartDemo(NULL) = %d, want MIX_ERR_INVALID_ARGUMENT", code)
	}
	if code := MixStopDemo(); code != 8 {
		t.Fatalf("MixStopDemo without a demo = %d, want MIX_ERR_NOT_STARTED", code)
	}
	testSetListeners(demoStateCtx, demoDiscoveryCtx, demoLogCtx)
	defer func() { MixSetStateListener(nil, 0); MixSetDiscoveryListener(nil, 0); MixSetLogSink(nil, 0, 0) }()
	out, code := testStartDemo(`{"version":0,"family":"xair"}`)
	var m struct {
		Family, Host string
		Port         int
	}
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || m.Host != "127.0.0.1" || m.Port == 0 {
		t.Fatalf("MixStartDemo = %d, %q", code, out)
	}
	defer MixStopDemo()
	if _, code := testStartDemo(`{"version":0,"family":"xair"}`); code != 9 {
		t.Fatalf("second MixStartDemo = %d, want MIX_ERR_ALREADY_STARTED", code)
	}
	session, code := testStart(fmt.Sprintf(`{"version":0,"family":%q,"host":%q,"port":%d,"meters":{"inputs":true}}`, m.Family, m.Host, m.Port))
	if code != 0 || !json.Valid([]byte(session)) || !strings.Contains(session, `"demo":true`) {
		t.Fatalf("MixStart = %d, %q", code, session)
	}
	waitFor(t, "a live demo state", func() bool {
		return anyContains(demoStateCtx, `"demo":true`) && anyContains(demoStateCtx, `"status":"live"`)
	})
	cmd := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":2},"field":"fader"},"value":-7,"origin":{"kind":"gesture","clientId":"abi-demo","seq":1}}`
	if code := testApply(cmd); code != 0 {
		t.Fatalf("MixApply = %d", code)
	}
	waitFor(t, "the echo", func() bool { return anyContains(demoStateCtx, `"clientId":"abi-demo"`) })
	waitFor(t, "an xair meter frame", func() bool { b, _ := testMeterFrame(536); return len(b) == 24+18*4 })
	if code := MixStopDemo(); code != 0 {
		t.Fatalf("MixStopDemo = %d", code)
	}
	waitFor(t, "the session stopped", func() bool { return anyContains(demoStateCtx, `"status":"stopped"`) })
	if code := MixStop(); code != 8 {
		t.Fatalf("MixStop after MixStopDemo = %d: the demo session must be gone", code)
	}
}
