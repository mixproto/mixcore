// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package api

import (
	"testing"

	"github.com/mixproto/mixcore/meters"
)

// The race detector changes allocation counts, hence the build tag.
func TestReadMeterFrame_ZeroAlloc(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()
	fr := meters.Frame{Seq: 1, Count: 32}
	eng.slot.Publish(&fr)
	buf := make([]byte, MeterFrameMaxBytes)
	if a := testing.AllocsPerRun(1000, func() { eng.ReadMeterFrame(buf) }); a != 0 {
		t.Fatalf("ReadMeterFrame allocates %v times per call, want 0", a)
	}
}
