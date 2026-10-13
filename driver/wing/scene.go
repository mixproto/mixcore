// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Scene-recall heuristic (wing.md section 7): the X32 burst rule. Whether
// the console pushes /$ctl/lib/$actidx or $active is unverified (item 17),
// so no marker is used yet and a recall is reported with low confidence.
// Design values.
const (
	burstParams    = 8                      // distinct parameters pushed ...
	burstWindow    = 250 * time.Millisecond // ... within this window make a burst
	sceneSettle    = 250 * time.Millisecond // report once no push came for this long
	sceneMaxWindow = time.Second            // ... or this long after the burst began
)

type burstEntry struct {
	idx int
	at  time.Time
}

// sceneState is the owner-goroutine state of the heuristic.
type sceneState struct {
	recent      []burstEntry
	seen        map[int]struct{}
	open        bool
	first, last time.Time
}

func newSceneState() sceneState { return sceneState{seen: map[int]struct{}{}} }

// onConsolePush records a console-origin push (never our echo or a read reply).
func (s *session) onConsolePush(idx int, at time.Time) {
	sc := &s.own.scene
	sc.recent = slices.DeleteFunc(append(sc.recent, burstEntry{idx: idx, at: at}),
		func(e burstEntry) bool { return at.Sub(e.at) > burstWindow })
	clear(sc.seen)
	for _, e := range sc.recent {
		sc.seen[e.idx] = struct{}{}
	}
	if len(sc.seen) < burstParams {
		return
	}
	if !sc.open {
		sc.open, sc.first = true, at
	}
	sc.last = at
}

// sceneTick reports a recall once its pushes settled, then re-reads the
// console at once (a recall may change parameters the console does not
// push, item 5). It never writes.
func (s *session) sceneTick(now time.Time) error {
	sc := &s.own.scene
	if !sc.open || (now.Sub(sc.last) < sceneSettle && now.Sub(sc.first) < sceneMaxWindow) {
		return nil
	}
	s.log.Info("wing: scene recalled at the console", "confidence", driver.ConfidenceLow)
	s.emit(driver.SceneRecalled{Confidence: driver.ConfidenceLow, Signals: []string{driver.SignalBurst},
		HighlightedScene: driver.NoScene, LoadedScene: driver.NoScene, At: sc.first})
	*sc = sceneState{seen: sc.seen}
	return s.startReread(now)
}
