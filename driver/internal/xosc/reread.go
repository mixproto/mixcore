// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// rereadBatch is how many reads one step of a background re-read sends. One
// step runs per Timing.PingEvery, so a pass is paced like the initial read
// (X32-F07) and spread over a few seconds.
const rereadBatch = ReadWindow

// rereadPass is the owner-goroutine state of a background re-read.
type rereadPass struct {
	active bool
	// wanted: a requested pass waits for RereadGap after endedAt.
	wanted  bool
	endedAt time.Time
	skip    map[int]bool // parameters left out of the current pass (held)
	next    int          // next parameter index to read
	// asked holds the parameters read in the current step. The value is true
	// once the read is superseded (see markTouched).
	asked map[int]bool
}

var _ driver.Rereader = (*session)(nil)

// Reread implements driver.Rereader: it asks the owner goroutine for one
// read-only pass over every parameter and returns at once.
func (s *session) Reread() error { return s.RereadExcept(nil) }

var _ driver.SelectiveRereader = (*session)(nil)

// RereadExcept implements driver.SelectiveRereader: one pass that leaves
// out the given parameters (held by a person). It returns at once.
func (s *session) RereadExcept(skip []model.ParamRef) error {
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	set := map[int]bool{}
	for _, p := range skip {
		if _, ok := s.params.byRef(p); ok {
			set[s.params.index(p)] = true
		}
	}
	s.mu.Lock()
	s.rrSkip = set
	s.mu.Unlock()
	signal(s.rereadReady)
	return nil
}

// requestReread starts a requested pass, or defers it while a pass runs
// or until RereadGap after the previous pass ended (onTick starts it).
func (s *session) requestReread(now time.Time) {
	if s.rr.active || (!s.rr.endedAt.IsZero() && now.Sub(s.rr.endedAt) < s.timing.RereadGap) {
		s.rr.wanted = true
		return
	}
	s.startReread(now)
}

// rereadDue starts a deferred pass once the gap after the last one passed.
func (s *session) rereadDue(now time.Time) {
	if s.rr.wanted && !s.rr.active && now.Sub(s.rr.endedAt) >= s.timing.RereadGap {
		s.rr.wanted = false
		s.startReread(now)
	}
}

// startReread begins a pass unless one is running.
func (s *session) startReread(now time.Time) {
	if s.rr.active {
		return
	}
	s.rr.active, s.rr.next = true, 0
	s.mu.Lock()
	s.rr.skip, s.rrSkip = s.rrSkip, nil
	s.mu.Unlock()
	s.passStarted()
	s.live.lastStep = now // this is the pass's first step: the next one waits PingEvery, or its replies would be dropped
	s.rereadStep(now)
}

// rereadStep sends the next batch of reads (X32-F04: a read is the address
// without arguments, never a write). Reads leave from the write socket, which
// never holds /xremote and so receives replies but no pushes (X32-F01,
// X32-F21): a reply can never be mistaken for a push or an echo. Replies of
// the previous step arrived long ago on a LAN; late ones are dropped.
func (s *session) rereadStep(now time.Time) {
	clear(s.rr.asked)
	if s.lim.evaluate { // the last batch had a whole step to answer
		s.lim.evaluate = false
		s.passFinished()
	}
	if !s.rr.active {
		return
	}
	for sent := 0; sent < rereadBatch && s.rr.next < len(s.params.all); s.rr.next++ {
		idx := s.rr.next
		if s.writtenRecently(idx, now) || s.isAbsent(s.params.all[idx]) || s.rr.skip[idx] {
			continue // a gesture is moving it (its echo keeps us in sync), or the console lacks it
		}
		s.sendRead(s.params.all[idx].addr)
		s.rr.asked[idx] = false
		sent++
	}
	if s.rr.next >= len(s.params.all) {
		s.rr.active, s.lim.evaluate, s.rr.endedAt = false, true, now
	}
}

// sendRead sends an argument-less read on the write socket.
func (s *session) sendRead(addr string) {
	pkt, err := osc.AppendString(s.tx[:0], addr)
	if err != nil {
		s.log.Error(s.name+": encode read", "addr", addr, "err", err)
		return
	}
	s.tx = pkt
	if err := s.c.sendCmd(pkt); err != nil {
		s.log.Debug(s.name+": re-read send failed", "addr", addr, "err", err)
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
// was lost, so it is a console change. A Gain we wrote since its last
// re-read is reported even unchanged, flagged Authoritative: whatever was
// pushed meanwhile, this is the console's value now.
func (s *session) rereadReply(idx int, v model.Value, at time.Time) (driver.ParamChanged, bool) {
	touched, ok := s.rr.asked[idx]
	if !ok || touched {
		return driver.ParamChanged{}, false
	}
	delete(s.rr.asked, idx)
	sent, wrote := s.unconfirmed[idx]
	delete(s.unconfirmed, idx)
	unchanged := idx < len(s.known) && s.known[idx].Equal(v)
	if unchanged && !wrote {
		return driver.ParamChanged{}, false
	}
	s.remember(idx, v)
	ownReadBack := wrote && s.isOwnValue(idx, sent, v) // our own write read back is no unpushed change
	if !unchanged && !ownReadBack && limitEvidence(s.params.all[idx]) {
		s.lim.passFound = true // the push of this change never reached us
	}
	return driver.ParamChanged{Param: s.params.all[idx].ref, Value: v, Origin: driver.Origin{Kind: driver.OriginConsole}, At: at,
		Authoritative: wrote, OwnWrite: ownReadBack}, true
}

// isOwnValue reports whether a re-read value is the last value we sent,
// within half a step of the parameter's law.
func (s *session) isOwnValue(idx int, sent osc.Arg, v model.Value) bool {
	p := s.params.all[idx]
	got, err := toWire(p, v)
	return err == nil && sameWire(p, sent, got)
}

// remember records the newest value seen for idx.
func (s *session) remember(idx int, v model.Value) {
	if idx < len(s.known) {
		s.known[idx] = v
	}
}
