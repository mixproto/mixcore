// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mobile

import "testing"

func TestDemoCallsReturnStableCodes(t *testing.T) {
	if err := StopDemo(); err == nil || err.Error() != ErrNotStarted {
		t.Fatalf("StopDemo without a demo = %v", err)
	}
	if _, err := StartDemo(`{"version":0,"family":"wing"}`); err == nil || err.Error() != ErrInvalidConfig {
		t.Fatalf("StartDemo of an unknown family = %v", err)
	}
}
