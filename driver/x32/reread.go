// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// rereadBatch is how many reads one step of a background re-read sends. One
// step runs per Timing.PingEvery, so a pass is paced like the initial read
// (X32-F07) and spread over a few seconds.
const rereadBatch = readWindow

// rereadPass is the owner-goroutine state of a background re-read.
type rereadPass struct {
	active bool
	next   int // next parameter index to read
	// asked holds the parameters read in the current step. The value is true
	// once the read is superseded (see markTouched).
	asked map[int]bool
}

var _ driver.Rereader = (*session)(nil)

// Reread implements driver.Rereader: it asks the owner goroutine for one
// read-only pass over every parameter and returns at once.
func (s *session) Reread() error {
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	signal(s.rereadReady)
	return nil
}

// startReread begins a pass unless one is running.
func (s *session) startReread(now time.Time) {
	if s.rr.active {
		return
	}
	s.rr.active, s.rr.next = true, 0
	s.rereadStep(now)
}

// rereadStep sends the next batch of reads (X32-F04: a read is the address
// without arguments, never a write). Reads leave from the write socket, which
// never holds /xremote and so receives replies but no pushes (X32-F01,
// X32-F21): a reply can never be mistaken for a push or an echo. Replies of
// the previous step arrived long ago on a LAN; late ones are dropped.
func (s *session) rereadStep(now time.Time) {
	clear(s.rr.asked)
	if !s.rr.active {
		return
	}
	for sent := 0; sent < rereadBatch && s.rr.next < len(s.params.all); s.rr.next++ {
		idx := s.rr.next
		if s.writtenRecently(idx, now) {
			continue // a gesture is moving it; its echo keeps us in sync
		}
		s.sendRead(s.params.all[idx].addr)
		s.rr.asked[idx] = false
		sent++
	}
	if s.rr.next >= len(s.params.all) {
		s.rr.active = false
	}
}

// sendRead sends an argument-less read on the write socket.
func (s *session) sendRead(addr string) {
	pkt, err := osc.AppendString(s.tx[:0], addr)
	if err != nil {
		s.log.Error("x32: encode read", "addr", addr, "err", err)
		return
	}
	s.tx = pkt
	if err := s.c.sendCmd(pkt); err != nil {
		s.log.Debug("x32: re-read send failed", "addr", addr, "err", err)
	}
}

// writtenRecently reports whether we wrote parameter idx within the echo window.
func (s *session) writtenRecently(idx int, now time.Time) bool {
	h := s.sent[idx]
	return len(h) > 0 && now.Sub(h[len(h)-1].at) <= s.timing.EchoWindow
}

// markTouched marks an outstanding re-read of idx as superseded: one of our
// writes left, or a push or echo arrived, after the read was sent. Its reply
// may then be older than what we know and is discarded.
func (s *session) markTouched(idx int) {
	if _, ok := s.rr.asked[idx]; ok {
		s.rr.asked[idx] = true
	}
}

// rereadReply handles a packet from the write socket, which can only be the
// reply to a re-read. It reports a change only for an outstanding, untouched
// read whose value differs from the newest one seen: the push of that change
// was lost, so it is a console change.
func (s *session) rereadReply(idx int, v model.Value, at time.Time) (driver.ParamChanged, bool) {
	touched, ok := s.rr.asked[idx]
	if !ok || touched {
		return driver.ParamChanged{}, false
	}
	delete(s.rr.asked, idx)
	if idx < len(s.known) && s.known[idx].Equal(v) {
		return driver.ParamChanged{}, false
	}
	s.remember(idx, v)
	return driver.ParamChanged{Param: s.params.all[idx].ref, Value: v, Origin: driver.Origin{Kind: driver.OriginConsole}, At: at}, true
}

// remember records the newest value seen for idx.
func (s *session) remember(idx int, v model.Value) {
	if idx < len(s.known) {
		s.known[idx] = v
	}
}
