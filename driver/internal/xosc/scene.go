// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Scene-recall heuristic (docs/protocols/x32.md X32-F60..F62, xair.md
// section 7). Design values, shared by both families.
const (
	burstParams    = 8                      // distinct parameters pushed ...
	burstWindow    = 250 * time.Millisecond // ... within this window make a burst
	sceneSettle    = 250 * time.Millisecond // report once no new signal came for this long
	sceneMaxWindow = time.Second            // ... or this long after the first signal
)

// MarkerKind says what a pushed scene marker means.
type MarkerKind uint8

// Marker kinds.
const (
	// MarkerRecall is a signal of its own (x32 /-undo/time, X32-F62).
	MarkerRecall MarkerKind = iota + 1
	// MarkerHighlight names the highlighted scene: a hint for a recall the
	// other signals found, never a signal of its own (x32
	// /-show/prepos/current, X32-F61).
	MarkerHighlight
	// MarkerIndexHint names the recently loaded or saved scene (xair
	// /-snap/index, XA-F60): a save moves it too, so it is a hint that only
	// enriches a recall the burst found (LoadedScene), never a signal alone.
	MarkerIndexHint
)

// Marker is one scene marker address of a family.
type Marker struct {
	Addr   string
	Kind   MarkerKind
	Signal string // driver.Signal* reported for a MarkerRecall or MarkerIndexHint
	// MinScene..MaxScene: the valid int argument of a MarkerIndexHint (xair:
	// 1..64, XA-F60); anything else is dropped, never guessed.
	MinScene, MaxScene int32
}

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
	burst       bool
	marker      string // signal of the recall marker pushed, empty for none
	scene       int    // highlighted scene pushed, or driver.NoScene
	sceneAt     time.Time
	loaded      int // loaded-or-saved scene pushed (index hint), or driver.NoScene
	loadedAt    time.Time
	loadedSig   string // signal of the index hint
}

func newSceneState() sceneState {
	return sceneState{seen: map[int]struct{}{}, scene: driver.NoScene, loaded: driver.NoScene}
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

// onMarker handles a push of one of the family's scene markers. It reports
// whether the message was one.
func (s *session) onMarker(at time.Time) bool {
	i := slices.IndexFunc(s.prof.Markers, func(m Marker) bool { return s.msg.AddressIs(m.Addr) })
	if i < 0 {
		return false
	}
	mk := s.prof.Markers[i]
	switch mk.Kind {
	case MarkerHighlight:
		if v, ok := s.msg.IntArg(0); ok {
			s.scene.scene, s.scene.sceneAt = int(v), at
		}
		s.onPush()
	case MarkerIndexHint:
		if v, ok := s.msg.IntArg(0); ok && v >= mk.MinScene && v <= mk.MaxScene {
			s.scene.loaded, s.scene.loadedAt, s.scene.loadedSig = int(v), at, mk.Signal
		}
		s.onPush() // a push all the same: we are served
	default:
		s.scene.marker = mk.Signal
		s.onPush()
		s.sceneSignal(at)
	}
	return true
}

func (s *session) sceneSignal(at time.Time) {
	if !s.scene.open {
		s.scene.open, s.scene.first = true, at
		if at.Sub(s.scene.sceneAt) > sceneMaxWindow {
			s.scene.scene = driver.NoScene // a highlight from browsing long before
		}
		if at.Sub(s.scene.loadedAt) > sceneMaxWindow {
			s.scene.loaded = driver.NoScene // an index from a save or load long before
		}
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
	ev := driver.SceneRecalled{Confidence: sceneConfidence(sc), Signals: sceneSignals(sc),
		HighlightedScene: sc.scene, LoadedScene: sc.loaded, At: sc.first}
	s.log.Info(s.name+": scene recalled at the console", "confidence", ev.Confidence, "signals", ev.Signals)
	s.emit(ev)
	*sc = sceneState{seen: sc.seen, scene: driver.NoScene, loaded: driver.NoScene}
	s.startReread(now)
}

func sceneConfidence(sc *sceneState) string {
	switch {
	case sc.burst && sc.marker != "":
		return driver.ConfidenceHigh
	case sc.marker != "":
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
	if sc.marker != "" {
		out = append(out, sc.marker)
	}
	if sc.scene != driver.NoScene {
		out = append(out, driver.SignalHighlightedScene)
	}
	if sc.loaded != driver.NoScene {
		out = append(out, sc.loadedSig)
	}
	return out
}
