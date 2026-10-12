// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Scene-recall heuristic (docs/protocols/x32.md, X32-F60..F62). Design values.
const (
	burstParams    = 8                       // distinct parameters pushed ...
	burstWindow    = 250 * time.Millisecond  // ... within this window make a burst
	sceneSettle    = 250 * time.Millisecond  // report once no new signal came for this long
	sceneMaxWindow = time.Second             // ... or this long after the first signal
	addrUndoTime   = "/-undo/time"           // X32-F62
	addrPrepos     = "/-show/prepos/current" // X32-F61: highlighted, not loaded
)

type burstEntry struct {
	idx int
	at  time.Time
}

// sceneState is the owner-goroutine state of the heuristic.
type sceneState struct {
	recent      []burstEntry // console-origin pushes within burstWindow
	seen        map[int]struct{}
	open        bool
	first, last time.Time
	burst, undo bool
	scene       int // highlighted scene pushed, or driver.NoScene
}

func newSceneState() sceneState {
	return sceneState{seen: map[int]struct{}{}, scene: driver.NoScene}
}

// onConsolePush records a console-origin push of parameter idx (never our echo).
func (s *session) onConsolePush(idx int, at time.Time) {
	sc := &s.scene
	sc.recent = slices.DeleteFunc(append(sc.recent, burstEntry{idx: idx, at: at}),
		func(e burstEntry) bool { return at.Sub(e.at) > burstWindow })
	clear(sc.seen)
	for _, e := range sc.recent {
		sc.seen[e.idx] = struct{}{}
	}
	if len(sc.seen) >= burstParams {
		sc.burst = true
		s.sceneSignal(at)
	}
}

// onMarker handles a push of a scene marker. It reports whether addr was one.
func (s *session) onMarker(at time.Time) bool {
	switch {
	case s.msg.AddressIs(addrUndoTime):
		s.scene.undo = true
	case s.msg.AddressIs(addrPrepos):
		v, ok := s.msg.IntArg(0)
		if !ok {
			return true
		}
		s.scene.scene = int(v)
	default:
		return false
	}
	s.onPush()
	s.sceneSignal(at)
	return true
}

func (s *session) sceneSignal(at time.Time) {
	if !s.scene.open {
		s.scene.open, s.scene.first = true, at
	}
	s.scene.last = at
}

// sceneTick reports a recall once its signals settled, then re-reads the
// console (a burst over Wi-Fi can lose packets). It never writes.
func (s *session) sceneTick(now time.Time) {
	sc := &s.scene
	if !sc.open || (now.Sub(sc.last) < sceneSettle && now.Sub(sc.first) < sceneMaxWindow) {
		return
	}
	ev := driver.SceneRecalled{Confidence: sceneConfidence(sc), Signals: sceneSignals(sc), HighlightedScene: sc.scene, At: sc.first}
	s.log.Info("x32: scene recalled at the console", "confidence", ev.Confidence, "signals", ev.Signals)
	s.emit(ev)
	*sc = sceneState{seen: sc.seen, scene: driver.NoScene}
	s.startReread(now)
}

func sceneConfidence(sc *sceneState) string {
	marker := sc.undo || sc.scene != driver.NoScene
	switch {
	case sc.burst && marker:
		return driver.ConfidenceHigh
	case marker:
		return driver.ConfidenceMedium
	default:
		return driver.ConfidenceLow
	}
}

func sceneSignals(sc *sceneState) []string {
	var out []string
	if sc.burst {
		out = append(out, driver.SignalBurst)
	}
	if sc.undo {
		out = append(out, driver.SignalUndoTime)
	}
	if sc.scene != driver.NoScene {
		out = append(out, driver.SignalHighlightedScene)
	}
	return out
}
