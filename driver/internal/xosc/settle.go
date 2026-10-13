// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import "time"

// Settle re-read (redesign CC-3, core-api v0.5 rule 8). Whether the console
// pushes our own Gain write back is unverified (X32-F79, XA-F116), so the
// console layer keeps every Gain value in flight until a report matches
// it. When the last Gain write of a burst got no echo within SettleAfter,
// the session reads that headamp once on the command socket; the reply is
// handled like a background re-read reply (rereadReply): authoritative,
// flagged OwnWrite when it is the value we sent. A write or push after the
// read was sent supersedes it (markTouched). Reads only, never a write.
// With the default timing the read leaves 100 to 200 ms after the last
// write (LivenessTick 100 ms), well inside the 250 ms of the design.

// settleState is the owner-goroutine state of the settle re-reads.
type settleState struct {
	due   map[int]time.Time // Gain parameter index -> time of its last write
	asked map[int]bool      // reads in flight; true once superseded
}

func newSettleState() settleState {
	return settleState{due: map[int]time.Time{}, asked: map[int]bool{}}
}

// noteGainWrite records a Gain write that left now.
func (s *session) noteGainWrite(idx int, now time.Time) {
	s.settle.due[idx] = now
}

// settleTick sends the settle reads that are due: the last write is at
// least SettleAfter old and its echo has not arrived.
func (s *session) settleTick(now time.Time) {
	for idx, at := range s.settle.due {
		if now.Sub(at) < s.timing.SettleAfter {
			continue
		}
		delete(s.settle.due, idx)
		if len(s.sent[idx]) == 0 {
			continue // echoed: matchEcho forgot the write and every older one
		}
		s.sendRead(s.params.all[idx].addr)
		s.settle.asked[idx] = false
	}
}

// takeSettleAsk reports whether a reply for idx answers an untouched
// settle read, and forgets the read.
func (s *session) takeSettleAsk(idx int) (asked, touched bool) {
	touched, asked = s.settle.asked[idx]
	delete(s.settle.asked, idx)
	return asked, touched
}
