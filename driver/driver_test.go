// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package driver

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestOrigin_MayWrite(t *testing.T) {
	tests := []struct {
		kind OriginKind
		want bool
	}{
		{OriginGesture, true},
		{OriginMacro, true},
		{OriginConsole, false},
		{OriginSystem, false},
		{0, false},
	}
	for _, tc := range tests {
		if got := (Origin{Kind: tc.kind}).MayWrite(); got != tc.want {
			t.Fatalf("Origin{%v}.MayWrite() = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestParseOriginKind(t *testing.T) {
	for _, k := range []OriginKind{OriginGesture, OriginMacro, OriginConsole, OriginSystem} {
		got, ok := ParseOriginKind(k.String())
		if !ok || got != k {
			t.Fatalf("ParseOriginKind(%q) = %v, %v", k.String(), got, ok)
		}
	}
	if _, ok := ParseOriginKind("timer"); ok {
		t.Fatal("unknown origin kinds must not parse")
	}
	if OriginKind(99).String() != "unknown" {
		t.Fatal("unknown kind must print as unknown")
	}
}
