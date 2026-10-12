// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"errors"
	"math"
	"testing"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/api"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestCodeNumbers_EveryCodeHasAUniqueNumber(t *testing.T) {
	all := []string{
		api.CodeInvalidConfig, api.CodeInvalidCommand, api.CodeInvalidValue, api.CodeNotUserOrigin, api.CodeStale,
		api.CodeGestureEnded, api.CodeUnsupported, api.CodeNotStarted, api.CodeAlreadyStarted, api.CodeInternal,
		api.CodeRole, api.CodeBusy, api.CodeNetwork, api.CodeInvalidArg,
	}
	seen := map[int32]string{}
	for i, c := range all {
		n, ok := codeNumbers[c]
		if !ok || n != int32(i+1) {
			t.Fatalf("%s = %d (mapped %v), want %d: the C ABI numbers are fixed", c, n, ok, i+1)
		}
		if prev, dup := seen[n]; dup {
			t.Fatalf("%s and %s share %d", c, prev, n)
		}
		seen[n] = c
	}
	if len(codeNumbers) != len(all) {
		t.Fatalf("%d codes mapped, %d listed", len(codeNumbers), len(all))
	}
}

func TestCodeNumber(t *testing.T) {
	if codeNumber(nil) != 0 {
		t.Fatal("nil must be MIX_OK")
	}
	if codeNumber(errors.New("not coded")) != codeNumbers[api.CodeInternal] {
		t.Fatal("an uncoded error must be MIX_ERR_INTERNAL")
	}
	if codeNumber(&api.Error{Code: api.CodeStale}) != 5 {
		t.Fatal("ERR_STALE must be 5")
	}
}

func TestExports_WithoutCgoArguments(t *testing.T) {
	if got := MixApply(nil); got != 14 {
		t.Fatalf("MixApply(NULL) = %d, want MIX_ERR_INVALID_ARGUMENT", got)
	}
	if got := MixDiscover(nil); got != 14 {
		t.Fatalf("MixDiscover(NULL) = %d", got)
	}
	if got := MixStart(nil, nil, nil); got != 14 {
		t.Fatalf("MixStart(NULL...) = %d", got)
	}
	if got := MixStop(); got != 8 {
		t.Fatalf("MixStop before start = %d, want MIX_ERR_NOT_STARTED", got)
	}
	if got := MixMeterFrame(nil, 0); got != 24 {
		t.Fatalf("size query = %d, want the empty frame size", got)
	}
	if got := MixMeterFrame(nil, 5); got != -14 {
		t.Fatalf("NULL buffer with capacity = %d, want -MIX_ERR_INVALID_ARGUMENT", got)
	}
	if got := MixFaderPosition(0); got != 0.75 {
		t.Fatalf("MixFaderPosition(0) = %v", got)
	}
	if got := MixFaderDB(0); !math.IsInf(float64(got), -1) {
		t.Fatalf("MixFaderDB(0) = %v", got)
	}
	if got := MixMeterPosition(-30); got != 0.5 {
		t.Fatalf("MixMeterPosition(-30) = %v", got)
	}
	if got := MixMeterRelease(-10, -40, 100); got != -12 {
		t.Fatalf("MixMeterRelease = %v", got)
	}
	if got := MixFaderStep(-60, -1); !math.IsInf(float64(got), -1) {
		t.Fatalf("MixFaderStep(-60, -1) = %v, want -inf", got)
	}
	if got := MixFaderStep(-75, 1); got != -74 {
		t.Fatalf("MixFaderStep(-75, +1) = %v", got)
	}
	if got := MixMeterScale(nil, nil); got != 14 {
		t.Fatalf("MixMeterScale(NULL) = %d", got)
	}
	if MixSetStateListener(nil, 0) != 0 || MixSetDiscoveryListener(nil, 0) != 0 || MixSetLogSink(nil, 0, 0) != 0 {
		t.Fatal("clearing callbacks must succeed")
	}
	MixFree(nil)
}
