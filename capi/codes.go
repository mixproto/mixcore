// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

// #include <stdint.h>
import "C"

import "github.com/mixproto/mixcore/api"

// codeNumber is the C status code of err (0 for nil).
func codeNumber(err error) int32 {
	if err == nil {
		return 0
	}
	if n, ok := codeNumbers[api.CodeOf(err)]; ok {
		return n
	}
	return codeNumbers[api.CodeInternal]
}

func codeFor(err error) C.int32_t { return C.int32_t(codeNumber(err)) }

// recoverCode turns a panic into MIX_ERR_INTERNAL; use with a named result.
func recoverCode(code *C.int32_t) {
	if recover() != nil {
		*code = C.int32_t(codeNumbers[api.CodeInternal])
	}
}
