// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"sync/atomic"

	"github.com/mixproto/mixcore/internal/capture/flow"
)

// frameSink hands recorded frames to the live analysis without ever waiting:
// forwarding a vendor app's traffic must not depend on how fast we analyze.
// A frame that finds the queue full is counted and dropped from the live
// view only; it is in capture.pcap, and "mixcapture report" re-analyzes it.
type frameSink struct {
	frames chan<- flow.Frame
	drops  atomic.Int64
}

func newFrameSink(frames chan<- flow.Frame) *frameSink { return &frameSink{frames: frames} }

func (s *frameSink) put(fr flow.Frame) {
	select {
	case s.frames <- fr:
	default:
		s.drops.Add(1)
	}
}

func (s *frameSink) dropped() int64 { return s.drops.Load() }
