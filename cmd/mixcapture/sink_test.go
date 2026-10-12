// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
)

// Regression (go-reviewer L4): handing a frame to live analysis never waits;
// with a full queue the frame is counted as dropped (it is still in the pcap).
func TestFrameSinkNeverBlocks(t *testing.T) {
	frames := make(chan flow.Frame, 1)
	sink := newFrameSink(frames)
	start := time.Now()
	for range 3 {
		sink.put(flow.Frame{})
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("put waited on a full queue")
	}
	if got := sink.dropped(); got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}
}
