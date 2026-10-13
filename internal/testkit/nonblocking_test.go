// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package testkit

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// The guard catches a call that blocks (here on a channel nobody sends
// on), and passes one that returns.
func TestReturnsWithin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		if err := ReturnsWithin(time.Second, func() { <-release }); !errors.Is(err, ErrBlocked) {
			t.Fatalf("a blocking call passed the guard: %v", err)
		}
		close(release)
		if err := ReturnsWithin(time.Second, func() {}); err != nil {
			t.Fatalf("a returning call: %v", err)
		}
		synctest.Wait()
	})
}
