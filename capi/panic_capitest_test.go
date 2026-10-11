// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build capitest

package main

import (
	"testing"

	"github.com/mixproto/mixcore/api"
)

func TestMixTestPanic_ComesBackAsInternalError(t *testing.T) {
	if got := MixTestPanic(); int32(got) != codeNumbers[api.CodeInternal] {
		t.Fatalf("MixTestPanic = %d, want MIX_ERR_INTERNAL", got)
	}
}
