// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Evidence thresholds of the client-limit detection (docs/protocols/x32.md,
// "Client limit detection"). Design values, tuned on hardware with mixprobe.
const (
	// limitAfterUnechoed value-changing writes without an echo, and no push
	// meanwhile, mean our /xremote is not served.
	limitAfterUnechoed = 3
	// limitAfterUnpushedPasses re-read passes in a row that found an
	// unpushed change, and no push meanwhile, mean the same.
	limitAfterUnpushedPasses = 2
)

// limitState is the owner-goroutine state of the detection.
type limitState struct {
	limited        bool
	unechoed       int  // writes whose echo window passed unmatched since the last push
	unpushedPasses int  // consecutive passes with an unpushed change
	passFound      bool // the current re-read pass found an unpushed change
	pushInPass     bool // a push arrived since the current pass started
	evaluate       bool // a finished pass waits for its verdict
}

// onPush records a push on the subscription socket: /xremote is served.
func (s *session) onPush() {
	s.lim.unechoed, s.lim.unpushedPasses, s.lim.pushInPass = 0, 0, true
	if s.lim.limited {
		s.lim.limited = false
		s.log.Info("x32: updates arrive again")
		s.emit(driver.ClientLimitCleared{})
		s.startReread(time.Now()) // changes made while we were not served were never pushed
	}
}

// expireUnechoed forgets writes whose echo window passed without an echo and
// counts those that changed a value (X32-F24: an unchanged value may never be
// pushed, so it proves nothing).
func (s *session) expireUnechoed(now time.Time) {
	for idx, h := range s.sent {
		keep := h[:0]
		for _, w := range h {
			switch {
			case now.Sub(w.at) <= s.timing.EchoWindow:
				keep = append(keep, w)
			case w.changes && w.evidence:
				s.lim.unechoed++
			}
		}
		if len(keep) == 0 {
			delete(s.sent, idx)
		} else {
			s.sent[idx] = keep
		}
	}
	if s.lim.unechoed >= limitAfterUnechoed {
		s.limit(driver.LimitNoEcho)
	}
}

// passStarted resets the per-pass evidence (called when a re-read pass starts).
func (s *session) passStarted() {
	s.lim.passFound, s.lim.pushInPass, s.lim.evaluate = false, false, false
}

// passFinished judges a re-read pass after its replies had time to arrive.
func (s *session) passFinished() {
	if s.lim.passFound && !s.lim.pushInPass {
		s.lim.unpushedPasses++
	} else {
		s.lim.unpushedPasses = 0
	}
	if s.lim.unpushedPasses >= limitAfterUnpushedPasses {
		s.limit(driver.LimitUnpushedChanges)
	}
}

func (s *session) limit(reason string) {
	if s.lim.limited {
		return
	}
	s.lim.limited = true
	s.log.Info("x32: console does not send us updates (client limit)", "reason", reason, "limit", maxPushClients)
	s.emit(driver.ClientLimitHit{Limit: maxPushClients, Reason: reason})
}
