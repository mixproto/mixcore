// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"fmt"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// liveness is the owner-goroutine state of the liveness rules (X32-F25).
type liveness struct {
	since     time.Time // Run start: no packet before it counts
	lastProbe time.Time // last /xinfo sent
	lastStep  time.Time // last background re-read step
}

func newLiveness(now time.Time) liveness {
	return liveness{since: now, lastProbe: now, lastStep: now}
}

// onTick applies the liveness rules, probes when due and paces the background
// re-read. It returns an error wrapping driver.ErrConnectionLost when the
// console is lost. Everything it sends is a read.
func (s *session) onTick(now time.Time) error {
	rx := s.c.lastRxAt()
	if rx.Before(s.live.since) {
		rx = s.live.since
	}
	if silent := now.Sub(rx); silent > s.timing.StaleAfter {
		return fmt.Errorf("%s: no packet for %s: %w", s.name, silent.Round(time.Millisecond), driver.ErrConnectionLost)
	}
	if silent := now.Sub(s.lastPong); silent > s.timing.DeadAfter {
		return fmt.Errorf("%s: no /xinfo reply for %s: %w", s.name, silent.Round(time.Millisecond), driver.ErrConnectionLost)
	}
	quiet := now.Sub(rx) >= s.timing.QuietProbeAfter && now.Sub(s.live.lastProbe) >= s.timing.QuietProbeAfter
	if quiet || now.Sub(s.live.lastProbe) >= s.timing.PingEvery {
		s.sendSub(addrXinfo) // a read, never a write
		s.live.lastProbe = now
	}
	s.settleTick(now)
	s.expireUnechoed(now)
	s.sceneTick(now)
	s.rereadDue(now)
	if now.Sub(s.live.lastStep) >= s.timing.PingEvery {
		s.live.lastStep = now
		s.rereadStep(now)
	}
	return nil
}
