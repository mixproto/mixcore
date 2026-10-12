// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package mobile is the only package bound with gomobile (iOS, iPadOS and
// macOS xcframework, Android AAR). It is a thin wrapper over package api: it
// only translates types, and every behaviour lives in api. The surface uses
// strings, ints, floats, bools, []byte, error and callback interfaces only
// (contracts/core-api/README.md, "Bindings").
//
// Rules kept here:
//   - One engine per process (one bound framework per app).
//   - No panic crosses the boundary: every function recovers and returns
//     ERR_INTERNAL, and every goroutine behind it runs under internal/safe.
//   - No call blocks on the network; callbacks come from engine goroutines and
//     must return quickly.
//   - Errors carry a stable code as their whole message (the Err* constants).
//   - MeterFrame returns a new slice that the binding copies again;
//     ReadMeterFrame writes into the caller's buffer during the call and
//     never retains it.
package mobile

import (
	"errors"

	"github.com/mixproto/mixcore/api"
)

// engine is the one engine of the process.
var engine = api.NewEngine()

// Stable error codes: the message of every error returned here is exactly one
// of these (contracts/core-api/README.md, "Error codes").
const (
	ErrInvalidConfig  = api.CodeInvalidConfig
	ErrInvalidCommand = api.CodeInvalidCommand
	ErrInvalidValue   = api.CodeInvalidValue
	ErrNotUserOrigin  = api.CodeNotUserOrigin
	ErrStale          = api.CodeStale
	ErrGestureEnded   = api.CodeGestureEnded
	ErrUnsupported    = api.CodeUnsupported
	ErrNotStarted     = api.CodeNotStarted
	ErrAlreadyStarted = api.CodeAlreadyStarted
	ErrInternal       = api.CodeInternal
	ErrRole           = api.CodeRole
	ErrBusy           = api.CodeBusy
	ErrNetwork        = api.CodeNetwork
	ErrNeedsConfirm   = api.CodeNeedsConfirm // v0.3
	ErrShowMode       = api.CodeShowMode     // v0.3
	ErrGainBound      = api.CodeGainBound    // v0.3
)

// Log levels for SetLogSink and LogSink.Log.
const (
	LogDebug = api.LogDebug
	LogInfo  = api.LogInfo
	LogWarn  = api.LogWarn
	LogError = api.LogError
)

// MeterFrameMaxBytes is the largest frame MeterFrame returns.
const MeterFrameMaxBytes = api.MeterFrameMaxBytes

// StateListener receives state messages (state.schema.json) in seq order from
// one goroutine. It must return quickly and must not call Stop or Pause.
type StateListener interface {
	OnState(stateJSON string)
}

// DiscoveryListener receives one discovery.schema.json message per Discover.
type DiscoveryListener interface {
	OnDiscovery(resultJSON string)
}

// LogSink receives log lines from one dedicated goroutine.
type LogSink interface {
	Log(level int, line string)
}

// SocketBinder binds each new socket to the chosen network (Android only).
type SocketBinder interface {
	BindSocket(fd int) error
}

var errInternal = errors.New(api.CodeInternal)

// recoverErr turns a panic into ERR_INTERNAL; use with a named error result.
func recoverErr(err *error) {
	if recover() != nil {
		*err = errInternal
	}
}

func recoverQuiet() { _ = recover() }

// Start begins connecting to the console in configJSON (config.schema.json)
// and returns the session description (session.schema.json) at once.
func Start(configJSON string) (session string, err error) {
	defer recoverErr(&err)
	return engine.Start(configJSON)
}

// Stop disconnects. Start may be called again.
func Stop() (err error) {
	defer recoverErr(&err)
	return engine.Stop()
}

// Pause ends subscriptions and meters (app to background). It never writes.
func Pause() (err error) {
	defer recoverErr(&err)
	return engine.Pause()
}

// Resume reconnects and re-reads the full state. It never writes.
func Resume() (err error) {
	defer recoverErr(&err)
	return engine.Resume()
}

// SetStateListener sets (or clears, with nil) the state listener; the next
// message is a full snapshot.
func SetStateListener(l StateListener) {
	defer recoverQuiet()
	engine.SetStateListener(l)
}

// RequestSnapshot makes the next state message a full snapshot.
func RequestSnapshot() (err error) {
	defer recoverErr(&err)
	return engine.RequestSnapshot()
}

// Apply carries one user gesture (command.schema.json). It returns at once.
func Apply(commandJSON string) (err error) {
	defer recoverErr(&err)
	return engine.Apply(commandJSON)
}

// Gesture carries one command like Apply and returns what the core did
// (contracts/core-api/gesture-result.schema.json, v0.3): the applied value,
// the state for the haptic and the gesture window. Never blocks.
func Gesture(commandJSON string) (result string, err error) {
	defer recoverErr(&err)
	return engine.Gesture(commandJSON)
}

// MeterFrame returns the newest meter frame (meter-frame.md).
func MeterFrame() (frame []byte) {
	defer func() {
		if recover() != nil {
			frame = nil
		}
	}()
	return engine.MeterFrame()
}

// ReadMeterFrame copies the newest meter frame (meter-frame.md) into dst and
// returns its length; nothing is allocated. When dst is shorter than the
// frame nothing is written: call again with at least the returned size
// (MeterFrameMaxBytes always fits). gomobile passes dst by reference: Swift
// must pass an NSMutableData of MeterFrameMaxBytes allocated once (an
// immutable Data is copied by the binding and the frame is lost); Kotlin and
// Java pass a ByteArray allocated once (the JNI layer copies the bytes back).
func ReadMeterFrame(dst []byte) (n int) {
	defer func() {
		if recover() != nil {
			n = 0
		}
	}()
	return engine.ReadMeterFrame(dst)
}

// FaderPosition maps dB (-inf allowed) to a fader position 0..1.
func FaderPosition(db float64) (pos float64) {
	defer recoverQuiet()
	return engine.FaderPosition(db)
}

// FaderDB maps a fader position 0..1 to dB (-inf at 0).
func FaderDB(position float64) (db float64) {
	defer recoverQuiet()
	return engine.FaderDB(position)
}

// MeterScale returns the meter display scale (meter-scale.schema.json).
func MeterScale() (scale string) {
	defer recoverQuiet()
	return engine.MeterScale()
}

// MeterPosition maps a level in dBFS to 0..1 on the meter scale.
func MeterPosition(dbfs float64) (pos float64) {
	defer recoverQuiet()
	return engine.MeterPosition(dbfs)
}

// MeterRelease is one meter ballistics step (meter-frame.md).
func MeterRelease(shownDB, inputDB, elapsedMs float64) (db float64) {
	defer recoverQuiet()
	return engine.MeterRelease(shownDB, inputDB, elapsedMs)
}

// FaderStep applies a discrete fader step with the bottom-of-scale rule
// (-inf <-> -60 dB, design CR-21).
func FaderStep(db, deltaDB float64) (next float64) {
	defer recoverQuiet()
	return engine.FaderStep(db, deltaDB)
}

// SetDiscoveryListener sets (or clears, with nil) the discovery listener.
func SetDiscoveryListener(l DiscoveryListener) {
	defer recoverQuiet()
	engine.SetDiscoveryListener(l)
}

// Discover starts one read-only probe (discover.schema.json) and returns at
// once; the result goes to the discovery listener. ERR_BUSY while one runs.
func Discover(requestJSON string) (err error) {
	defer recoverErr(&err)
	return engine.Discover(requestJSON)
}

// SetLogSink sets (or clears, with nil) the log sink for lines at minLevel
// (LogDebug, LogInfo, LogWarn, LogError) and above.
func SetLogSink(sink LogSink, minLevel int) {
	defer recoverQuiet()
	engine.SetLogSink(sink, minLevel)
}

// SetSocketBinder sets (or clears, with nil) the socket binder. Android calls
// it before Start and Discover; Apple apps never call it.
func SetSocketBinder(b SocketBinder) {
	defer recoverQuiet()
	engine.SetSocketBinder(b)
}
