// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command capi is the c-shared build of the core API for Windows (mixcore.dll,
// called from C# through P/Invoke) and for interop tests on macOS and Linux
// (libmixcore.dylib / .so). It is the only //export package and a thin wrapper
// over package api; the C ABI is contracts/core-api/capi.md.
//
// Rules kept here: UTF-8 NUL-terminated strings in (copied on entry); results
// out through out-parameters, allocated with malloc and released by the caller
// with MixFree; int32 status codes (MIX_OK or MIX_ERR_*); no panic crosses the
// boundary (every export recovers, every goroutine runs under internal/safe);
// no call blocks on the network; callbacks are C function pointers registered
// once, called from one engine goroutine with a pointer that is valid only
// during the call.
package main

/*
#include <stdint.h>
#include <stdlib.h>

// State and discovery messages: UTF-8 JSON, not NUL-terminated, valid only during the call.
typedef void (*MixBytesCallback)(const uint8_t* data, int32_t length, intptr_t context);
// Log lines: level is MIX_LOG_*; line is UTF-8, not NUL-terminated, valid only during the call.
typedef void (*MixLogCallback)(int32_t level, const uint8_t* line, int32_t length, intptr_t context);

// Status codes (contracts/core-api/capi.md). MixMeterFrame returns the negated code on failure.
#define MIX_OK 0
#define MIX_ERR_INVALID_CONFIG 1
#define MIX_ERR_INVALID_COMMAND 2
#define MIX_ERR_INVALID_VALUE 3
#define MIX_ERR_NOT_USER_ORIGIN 4
#define MIX_ERR_STALE 5
#define MIX_ERR_GESTURE_ENDED 6
#define MIX_ERR_UNSUPPORTED 7
#define MIX_ERR_NOT_STARTED 8
#define MIX_ERR_ALREADY_STARTED 9
#define MIX_ERR_INTERNAL 10
#define MIX_ERR_ROLE 11
#define MIX_ERR_BUSY 12
#define MIX_ERR_NETWORK 13
#define MIX_ERR_INVALID_ARGUMENT 14

// Log levels (log/slog values).
#define MIX_LOG_DEBUG (-4)
#define MIX_LOG_INFO 0
#define MIX_LOG_WARN 4
#define MIX_LOG_ERROR 8

// A buffer of this size always holds a meter frame (meter-frame.md).
#define MIX_METER_FRAME_MAX_BYTES 536
*/
import "C"

import (
	"unsafe"

	"github.com/mixproto/mixcore/api"
)

// codeNumbers maps the stable string codes of api to the C status codes
// defined above, which are part of the C ABI: never renumber.
var codeNumbers = map[string]int32{
	api.CodeInvalidConfig:  C.MIX_ERR_INVALID_CONFIG,
	api.CodeInvalidCommand: C.MIX_ERR_INVALID_COMMAND,
	api.CodeInvalidValue:   C.MIX_ERR_INVALID_VALUE,
	api.CodeNotUserOrigin:  C.MIX_ERR_NOT_USER_ORIGIN,
	api.CodeStale:          C.MIX_ERR_STALE,
	api.CodeGestureEnded:   C.MIX_ERR_GESTURE_ENDED,
	api.CodeUnsupported:    C.MIX_ERR_UNSUPPORTED,
	api.CodeNotStarted:     C.MIX_ERR_NOT_STARTED,
	api.CodeAlreadyStarted: C.MIX_ERR_ALREADY_STARTED,
	api.CodeInternal:       C.MIX_ERR_INTERNAL,
	api.CodeRole:           C.MIX_ERR_ROLE,
	api.CodeBusy:           C.MIX_ERR_BUSY,
	api.CodeNetwork:        C.MIX_ERR_NETWORK,
	api.CodeInvalidArg:     C.MIX_ERR_INVALID_ARGUMENT,
}

// engine is the one engine of the process (the Go runtime is never unloaded).
var engine = api.NewEngine()

func main() {}

// MixStart begins connecting (config.schema.json) and writes the session
// description (session.schema.json) to *outJSON / *outLen; free it with MixFree.
//
//export MixStart
func MixStart(configJSON *C.char, outJSON **C.char, outLen *C.int32_t) (code C.int32_t) {
	defer recoverCode(&code)
	if configJSON == nil || outJSON == nil || outLen == nil {
		return C.MIX_ERR_INVALID_ARGUMENT
	}
	*outJSON, *outLen = nil, 0
	session, err := engine.Start(C.GoString(configJSON))
	if err != nil {
		return codeFor(err)
	}
	*outJSON = C.CString(session)
	*outLen = C.int32_t(len(session))
	return C.MIX_OK
}

// MixStop disconnects. MixStart may be called again.
//
//export MixStop
func MixStop() (code C.int32_t) {
	defer recoverCode(&code)
	return codeFor(engine.Stop())
}

// MixPause ends subscriptions and meters. It never writes.
//
//export MixPause
func MixPause() (code C.int32_t) {
	defer recoverCode(&code)
	return codeFor(engine.Pause())
}

// MixResume reconnects and re-reads the full state. It never writes.
//
//export MixResume
func MixResume() (code C.int32_t) {
	defer recoverCode(&code)
	return codeFor(engine.Resume())
}

// MixRequestSnapshot makes the next state message a full snapshot.
//
//export MixRequestSnapshot
func MixRequestSnapshot() (code C.int32_t) {
	defer recoverCode(&code)
	return codeFor(engine.RequestSnapshot())
}

// MixApply carries one user gesture (command.schema.json) and returns at once.
//
//export MixApply
func MixApply(commandJSON *C.char) (code C.int32_t) {
	defer recoverCode(&code)
	if commandJSON == nil {
		return C.MIX_ERR_INVALID_ARGUMENT
	}
	return codeFor(engine.Apply(C.GoString(commandJSON)))
}

// MixMeterFrame copies the newest meter frame (meter-frame.md) into the
// caller's buffer without allocating and returns its length. When capacity is
// smaller than that length nothing is written (snprintf style): call again with
// at least the returned size; MIX_METER_FRAME_MAX_BYTES always fits. A NULL
// buffer with capacity 0 only asks for the size. Failure: -MIX_ERR_*.
//
//export MixMeterFrame
func MixMeterFrame(buf *C.uint8_t, capacity C.int32_t) (n C.int32_t) {
	defer func() {
		if recover() != nil {
			n = -C.MIX_ERR_INTERNAL
		}
	}()
	if capacity < 0 || (buf == nil && capacity != 0) {
		return -C.MIX_ERR_INVALID_ARGUMENT
	}
	var dst []byte
	if buf != nil {
		dst = unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(capacity))
	}
	return C.int32_t(engine.ReadMeterFrame(dst))
}

// MixFaderPosition maps dB (-inf allowed) to a fader position 0..1.
//
//export MixFaderPosition
func MixFaderPosition(db C.double) (pos C.double) {
	defer func() { _ = recover() }()
	return C.double(engine.FaderPosition(float64(db)))
}

// MixFaderDB maps a fader position 0..1 to dB (-inf at 0).
//
//export MixFaderDB
func MixFaderDB(position C.double) (db C.double) {
	defer func() { _ = recover() }()
	return C.double(engine.FaderDB(float64(position)))
}

// MixMeterScale writes the meter display scale (meter-scale.schema.json) to
// *outJSON / *outLen; free it with MixFree.
//
//export MixMeterScale
func MixMeterScale(outJSON **C.char, outLen *C.int32_t) (code C.int32_t) {
	defer recoverCode(&code)
	if outJSON == nil || outLen == nil {
		return C.MIX_ERR_INVALID_ARGUMENT
	}
	scale := engine.MeterScale()
	*outJSON = C.CString(scale)
	*outLen = C.int32_t(len(scale))
	return C.MIX_OK
}

// MixMeterPosition maps a level in dBFS to 0..1 on the meter scale.
//
//export MixMeterPosition
func MixMeterPosition(dbfs C.double) (pos C.double) {
	defer func() { _ = recover() }()
	return C.double(engine.MeterPosition(float64(dbfs)))
}

// MixMeterRelease is one meter ballistics step (meter-frame.md).
//
//export MixMeterRelease
func MixMeterRelease(shownDB, inputDB, elapsedMs C.double) (db C.double) {
	defer func() { _ = recover() }()
	return C.double(engine.MeterRelease(float64(shownDB), float64(inputDB), float64(elapsedMs)))
}

// MixFaderStep applies a discrete fader step with the bottom-of-scale rule
// (-inf <-> -60 dB, design CR-21).
//
//export MixFaderStep
func MixFaderStep(db, deltaDB C.double) (next C.double) {
	defer func() { _ = recover() }()
	return C.double(engine.FaderStep(float64(db), float64(deltaDB)))
}

// MixSetStateListener registers (or, with NULL, clears) the state callback.
// The next message is a full snapshot.
//
//export MixSetStateListener
func MixSetStateListener(cb C.MixBytesCallback, context C.intptr_t) (code C.int32_t) {
	defer recoverCode(&code)
	if cb == nil {
		engine.SetStateListener(nil)
		return C.MIX_OK
	}
	engine.SetStateListener(bytesListener{cb: unsafe.Pointer(cb), ctx: context})
	return C.MIX_OK
}

// MixSetDiscoveryListener registers (or, with NULL, clears) the discovery callback.
//
//export MixSetDiscoveryListener
func MixSetDiscoveryListener(cb C.MixBytesCallback, context C.intptr_t) (code C.int32_t) {
	defer recoverCode(&code)
	if cb == nil {
		engine.SetDiscoveryListener(nil)
		return C.MIX_OK
	}
	engine.SetDiscoveryListener(bytesListener{cb: unsafe.Pointer(cb), ctx: context})
	return C.MIX_OK
}

// MixDiscover starts one read-only probe (discover.schema.json) and returns at
// once; the result goes to the discovery callback. MIX_ERR_BUSY while one runs.
//
//export MixDiscover
func MixDiscover(requestJSON *C.char) (code C.int32_t) {
	defer recoverCode(&code)
	if requestJSON == nil {
		return C.MIX_ERR_INVALID_ARGUMENT
	}
	return codeFor(engine.Discover(C.GoString(requestJSON)))
}

// MixSetLogSink registers (or, with NULL, clears) the log callback for lines at
// minLevel (MIX_LOG_*) and above.
//
//export MixSetLogSink
func MixSetLogSink(cb C.MixLogCallback, context C.intptr_t, minLevel C.int32_t) (code C.int32_t) {
	defer recoverCode(&code)
	if cb == nil {
		engine.SetLogSink(nil, int(minLevel))
		return C.MIX_OK
	}
	engine.SetLogSink(logSink{cb: unsafe.Pointer(cb), ctx: context}, int(minLevel))
	return C.MIX_OK
}

// MixFree releases memory returned by this library (NULL is ignored).
//
//export MixFree
func MixFree(p unsafe.Pointer) {
	C.free(p)
}
