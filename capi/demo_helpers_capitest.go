// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build capitest

package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

// testStartDemo calls MixStartDemo with C strings and frees the result with MixFree.
func testStartDemo(request string) (string, int32) {
	req := C.CString(request)
	defer C.free(unsafe.Pointer(req))
	var out *C.char
	var n C.int32_t
	code := MixStartDemo(req, &out, &n)
	if out == nil {
		return "", int32(code)
	}
	defer MixFree(unsafe.Pointer(out))
	return C.GoStringN(out, n), int32(code)
}
