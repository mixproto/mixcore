// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"bytes"
	"runtime"
	"strconv"
)

// Stop, Pause and StopDemo wait for the dispatcher goroutine; called from
// inside OnState they would wait for themselves. The dispatcher marks the
// goroutine it runs OnState on, and those calls return ERR_BUSY on that
// goroutine instead (contracts/core-api README, Threading). Calls from any
// other goroutine still wait as before.

// stackHeaderBytes holds "goroutine <id> [" of runtime.Stack.
const stackHeaderBytes = 64

// goroutineID is the runtime's id of the calling goroutine (0 if unreadable).
// Go has no API for it; the first line of runtime.Stack is "goroutine N [...".
func goroutineID() int64 {
	var buf [stackHeaderBytes]byte
	b := bytes.TrimPrefix(buf[:runtime.Stack(buf[:], false)], []byte("goroutine "))
	end := bytes.IndexByte(b, ' ')
	if end < 0 {
		return 0
	}
	id, err := strconv.ParseInt(string(b[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// inOnState reports whether the caller runs inside this engine's OnState.
func (e *Engine) inOnState() bool {
	id := e.onStateGoroutine.Load()
	return id != 0 && id == goroutineID()
}
