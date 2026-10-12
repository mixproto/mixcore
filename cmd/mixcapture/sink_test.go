// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/capture/pcapfile"
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

// Regression (go-reviewer nit 2): in live mode the pcap was written through a
// TeeReader inside the frame reader, so a stalled analysis stopped the copy:
// tcpdump backed up and the kernel dropped packets before they reached
// capture.pcap. With a consumer that never reads, the whole stream must
// still land in the file, at once, and the frames that did not fit counted.
func TestPumpCaptureWithAStuckConsumer(t *testing.T) {
	const packets = 3000 // ~240 KB, far more than any read buffer
	var src bytes.Buffer
	w, err := pcapfile.NewWriter(&src, netpkt.LinkRaw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range packets {
		if _, err := w.WritePacket(time.Unix(1_700_000_000, int64(i)), bytes.Repeat([]byte{byte(i)}, 64)); err != nil {
			t.Fatal(err)
		}
	}
	want := src.Bytes()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	frames := make(chan flow.Frame, 1) // nobody reads it: analysis is stuck
	sink := newFrameSink(frames)
	var file bytes.Buffer
	start := time.Now()
	err = pumpCapture(ctx, bytes.NewReader(want), &file, sink)
	if err != nil || ctx.Err() != nil {
		t.Fatalf("pumpCapture = %v after %s (context %v): it waited on the stuck consumer", err, time.Since(start), ctx.Err())
	}
	if !bytes.Equal(file.Bytes(), want) {
		t.Fatalf("capture file has %d of %d bytes", file.Len(), len(want))
	}
	if got := sink.dropped(); got != packets-1 {
		t.Fatalf("dropped = %d, want %d", got, packets-1)
	}
}
