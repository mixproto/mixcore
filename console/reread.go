// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"slices"
	"strings"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
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

// syncHeldLocked tells a session that implements driver.HeldSetter the
// parameters held now, when they changed, so a re-read pass already
// running leaves a fresh hold out from its next batch (review Go S3).
// SetHeld never blocks and never calls back. Callers hold mu.
func (s *Supervisor) syncHeldLocked() {
	hs, ok := s.sess.(driver.HeldSetter)
	if !ok {
		return
	}
	held := make([]model.ParamRef, 0, len(s.holds.active))
	for k := range s.holds.active {
		if !slices.Contains(held, k.param) {
			held = append(held, k.param)
		}
	}
	slices.SortFunc(held, func(a, b model.ParamRef) int { return strings.Compare(a.String(), b.String()) })
	if s.heldSent != nil && slices.Equal(held, *s.heldSent) {
		return
	}
	s.heldSent = &held
	hs.SetHeld(held)
}

// reread asks the session for one read-only pass while live. Held
// parameters are left out (a resting finger keeps holdBegin open from
// touch-down, and a re-read must not end that gesture); every other
// parameter is still recovered. A session that cannot leave parameters out
// skips the pass while anything is held. It never writes.
func (s *Supervisor) reread(rr driver.Rereader) {
	s.mu.Lock()
	live := s.status == StatusLive
	held := make([]model.ParamRef, 0, len(s.holds.active))
	for k := range s.holds.active {
		held = append(held, k.param)
	}
	s.mu.Unlock()
	var err error
	switch sel, ok := rr.(driver.SelectiveRereader); {
	case !live:
		return
	case ok:
		err = sel.RereadExcept(held)
	case len(held) > 0:
		return
	default:
		err = rr.Reread()
	}
	if err != nil {
		s.cfg.Logger.Debug("console: re-read", "err", err)
	}
}
