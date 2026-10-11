// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

/*
#include <stdint.h>

// Go cannot call a C function pointer; these trampolines do. They live apart
// from the //export file because its preamble may only hold declarations.
static inline void mix_call_bytes(void* cb, const uint8_t* p, int32_t n, intptr_t ctx) {
	((void (*)(const uint8_t*, int32_t, intptr_t))cb)(p, n, ctx);
}
static inline void mix_call_log(void* cb, int32_t level, const uint8_t* p, int32_t n, intptr_t ctx) {
	((void (*)(int32_t, const uint8_t*, int32_t, intptr_t))cb)(level, p, n, ctx);
}
*/
import "C"

import (
	"math"
	"unsafe"
)

// stringArg passes s to C without copying: Go memory without Go pointers,
// valid for the duration of the call only (cgo pointer rules). The callback
// must copy it before returning. Longer strings are cut at the int32 limit.
func stringArg(s string) (*C.uint8_t, C.int32_t) {
	n := min(len(s), math.MaxInt32)
	if n == 0 {
		return nil, 0
	}
	return (*C.uint8_t)(unsafe.Pointer(unsafe.StringData(s))), C.int32_t(n)
}

// bytesListener adapts a MixBytesCallback to api.StateListener and
// api.DiscoveryListener. The engine calls it from one goroutine.
type bytesListener struct {
	cb  unsafe.Pointer
	ctx C.intptr_t
}

func (l bytesListener) OnState(msg string)     { l.call(msg) }
func (l bytesListener) OnDiscovery(msg string) { l.call(msg) }

func (l bytesListener) call(msg string) {
	p, n := stringArg(msg)
	C.mix_call_bytes(l.cb, p, n, l.ctx)
}

// logSink adapts a MixLogCallback to api.LogSink.
type logSink struct {
	cb  unsafe.Pointer
	ctx C.intptr_t
}

func (s logSink) Log(level int, line string) {
	p, n := stringArg(line)
	C.mix_call_log(s.cb, C.int32_t(level), p, n, s.ctx)
}
