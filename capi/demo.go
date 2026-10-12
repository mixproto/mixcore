// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import "github.com/mixproto/mixcore/api"

// MixStartDemo starts the built-in demo mixer (demo.schema.json) and writes
// where it listens (demo-mixer.schema.json) to *outJSON / *outLen on MIX_OK;
// free it with MixFree. Pass its family, host and port to MixStart.
//
//export MixStartDemo
func MixStartDemo(requestJSON *C.char, outJSON **C.char, outLen *C.int32_t) (code C.int32_t) {
	defer recoverCode(&code)
	if requestJSON == nil || outJSON == nil || outLen == nil {
		return C.int32_t(codeNumbers[api.CodeInvalidArg])
	}
	*outJSON, *outLen = nil, 0
	mixer, err := engine.StartDemo(C.GoString(requestJSON))
	if err != nil {
		return codeFor(err)
	}
	*outJSON = C.CString(mixer)
	*outLen = C.int32_t(len(mixer))
	return 0 // MIX_OK
}

// MixStopDemo stops a session to the demo mixer, then the demo mixer.
//
//export MixStopDemo
func MixStopDemo() (code C.int32_t) {
	defer recoverCode(&code)
	return codeFor(engine.StopDemo())
}
