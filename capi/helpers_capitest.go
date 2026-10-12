// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build capitest

package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef void (*MixBytesCallback)(const uint8_t* data, int32_t length, intptr_t context);
typedef void (*MixLogCallback)(int32_t level, const uint8_t* line, int32_t length, intptr_t context);
extern void mixTestOnBytes(uint8_t* data, int32_t length, intptr_t context);
extern void mixTestOnLog(int32_t level, uint8_t* line, int32_t length, intptr_t context);
static inline MixBytesCallback mix_test_bytes_cb(void) { return (MixBytesCallback)mixTestOnBytes; }
static inline MixLogCallback mix_test_log_cb(void) { return (MixLogCallback)mixTestOnLog; }
*/
import "C"

import (
	"sync"
	"unsafe"
)

// Test-only helpers (capitest build): drive the exports with real C memory and
// C function pointers from Go tests, which cannot import "C" themselves.

var (
	testMu   sync.Mutex
	testMsgs = map[int][]string{}
)

func testMessages(ctx int) []string {
	testMu.Lock()
	defer testMu.Unlock()
	return append([]string(nil), testMsgs[ctx]...)
}

func record(ctx int, s string) {
	testMu.Lock()
	testMsgs[ctx] = append(testMsgs[ctx], s)
	testMu.Unlock()
}

//export mixTestOnBytes
func mixTestOnBytes(data *C.uint8_t, length C.int32_t, ctx C.intptr_t) {
	record(int(ctx), C.GoStringN((*C.char)(unsafe.Pointer(data)), C.int(length)))
}

//export mixTestOnLog
func mixTestOnLog(_ C.int32_t, line *C.uint8_t, length C.int32_t, ctx C.intptr_t) {
	record(int(ctx), C.GoStringN((*C.char)(unsafe.Pointer(line)), C.int(length)))
}

func testSetListeners(stateCtx, discoveryCtx, logCtx int) {
	MixSetStateListener(C.mix_test_bytes_cb(), C.intptr_t(stateCtx))
	MixSetDiscoveryListener(C.mix_test_bytes_cb(), C.intptr_t(discoveryCtx))
	MixSetLogSink(C.mix_test_log_cb(), C.intptr_t(logCtx), 0) // MIX_LOG_INFO
}

// testStart calls MixStart with C strings and frees the result with MixFree.
func testStart(config string) (string, int32) {
	cfg := C.CString(config)
	defer C.free(unsafe.Pointer(cfg))
	var out *C.char
	var n C.int32_t
	code := MixStart(cfg, &out, &n)
	if out == nil {
		return "", int32(code)
	}
	defer MixFree(unsafe.Pointer(out))
	return C.GoStringN(out, C.int(n)), int32(code)
}

func testCall(f func(*C.char) C.int32_t, s string) int32 {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return int32(f(cs))
}

func testApply(cmd string) int32    { return testCall(MixApply, cmd) }

// testGesture calls MixGesture with C memory and frees the result with MixFree.
func testGesture(cmd string) (string, int32) {
	c := C.CString(cmd)
	defer C.free(unsafe.Pointer(c))
	var out *C.char
	var n C.int32_t
	code := MixGesture(c, &out, &n)
	if out == nil {
		return "", int32(code)
	}
	defer MixFree(unsafe.Pointer(out))
	return C.GoStringN(out, C.int(n)), int32(code)
}
func testDiscover(req string) int32 { return testCall(MixDiscover, req) }

// testMeterFrame reads a frame into C memory of the given capacity.
func testMeterFrame(capacity int) ([]byte, int32) {
	buf := (*C.uint8_t)(C.malloc(C.size_t(max(capacity, 1))))
	defer C.free(unsafe.Pointer(buf))
	n := MixMeterFrame(buf, C.int32_t(capacity))
	if n <= 0 || int(n) > capacity {
		return nil, int32(n)
	}
	return C.GoBytes(unsafe.Pointer(buf), C.int(n)), int32(n)
}
