// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package testkit

import (
	"errors"
	"time"
)

// ErrBlocked: a call that must return at once did not (driver.Session's
// non-blocking contract).
var ErrBlocked = errors.New("testkit: call blocked")

// ReturnsWithin runs call on a goroutine and reports ErrBlocked when it has
// not returned within d. Inside a testing/synctest bubble a call blocked on
// a channel lets the fake clock run to d at once. A blocked call's
// goroutine stays blocked: the test must release it before it ends.
func ReturnsWithin(d time.Duration, call func()) error {
	done := make(chan struct{})
	go func() { defer close(done); call() }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return nil
	case <-t.C:
		return ErrBlocked
	}
}
