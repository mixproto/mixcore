// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"testing"
	"time"
)

// TestSceneBurst_BoundedUnderFlood (review sec M3): a flood of pushes of a
// few parameters keeps the burst bookkeeping bounded by the parameters.
func TestSceneBurst_BoundedUnderFlood(t *testing.T) {
	s := &session{scene: newSceneState()}
	at := time.Unix(1000, 0)
	for i := range 5000 {
		s.onConsolePush(i%3, at)
	}
	if n := s.scene.window.Len(); n > 2*3+16 {
		t.Fatalf("window holds %d entries for 3 parameters", n)
	}
	if s.scene.open {
		t.Fatal("3 parameters are no burst")
	}
}
