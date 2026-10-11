// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"time"

	"github.com/mixproto/mixcore/driver"
)

// DefaultRereadEvery is how often a live session re-reads the console state in
// the background, to recover changes whose push was lost. It is slow on
// purpose: a re-read is a burst of reads and the pushes already carry nearly
// every change. Design default, not a protocol fact.
const DefaultRereadEvery = 15 * time.Second

// rereadTicker returns the tick channel of the background re-read for sess,
// and a stop function. The channel is nil (never fires) when the session
// cannot re-read or re-reading is turned off.
func (s *Supervisor) rereadTicker(sess driver.Session) (driver.Rereader, <-chan time.Time, func()) {
	rr, ok := sess.(driver.Rereader)
	if !ok || s.cfg.RereadEvery < 0 {
		return nil, nil, func() {}
	}
	t := time.NewTicker(s.cfg.RereadEvery)
	return rr, t.C, t.Stop
}

// reread asks the session for one read-only pass, unless the state is not
// live or a person is holding a fader: a pass can then only end that gesture
// on a value we already know. It never writes.
func (s *Supervisor) reread(rr driver.Rereader) {
	s.mu.Lock()
	skip := s.status != StatusLive || len(s.holds.active) > 0
	s.mu.Unlock()
	if skip {
		return
	}
	if err := rr.Reread(); err != nil {
		s.cfg.Logger.Debug("console: re-read", "err", err)
	}
}
