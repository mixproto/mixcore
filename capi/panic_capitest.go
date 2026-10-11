// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build capitest

package main

// #include <stdint.h>
import "C"

// MixTestPanic panics inside an export on purpose, so interop tests prove that
// a Go panic comes back as MIX_ERR_INTERNAL instead of ending the process.
// Built only with -tags capitest; never in a shipped library.
//
//export MixTestPanic
func MixTestPanic() (code C.int32_t) {
	defer recoverCode(&code)
	panic("capitest: deliberate panic")
}
