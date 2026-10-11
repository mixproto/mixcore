// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package safe turns panics into errors. A panic in any goroutine of an app
// that embeds mixcore would kill the whole app, so every goroutine mixcore
// starts runs through Go, and every exported API entry point defers
// RecoverAsError.
package safe

import (
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

// ErrPanic marks an error that was produced by a recovered panic.
var ErrPanic = errors.New("panic recovered")

// Go runs fn on a new goroutine tracked by wg. A panic inside fn is recovered
// and passed to onPanic (which may be nil) as an error wrapping ErrPanic.
func Go(wg *sync.WaitGroup, onPanic func(error), fn func()) {
	wg.Go(func() {
		defer func() {
			if r := recover(); r != nil && onPanic != nil {
				onPanic(panicError(r))
			}
		}()
		fn()
	})
}

// RecoverAsError recovers a panic and stores it in *err. Use as
// `defer safe.RecoverAsError(&err)` in functions with a named error result.
func RecoverAsError(err *error) {
	if r := recover(); r != nil {
		*err = panicError(r)
	}
}

func panicError(r any) error {
	return fmt.Errorf("%w: %v\n%s", ErrPanic, r, debug.Stack())
}
