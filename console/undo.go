// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"maps"
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Undo history (redesign CC-6, core-api v0.5 rule 14, SR-13 and SR-29
// amended). Per client the core keeps what that client changed, newest
// last: a hold that wrote is one entry from its grab value to its last
// write; a set outside a hold is one entry (non-typed sets of one parameter
// less than 1 s apart merge: the -1 / +1 steppers); a toggle is one entry.
// An undo writes the newest entry's from value once. There is no age rule;
// an undo asks (ERR_NEEDS_CONFIRM) when it unmutes, raises a level by more
// than 6 dB, raises a level the entry lowered less than 30 s ago, meets a
// value changed elsewhere, crosses a scene recall at the console, or moves
// Gain more than 6 dB. Design values (fader-and-gain.md 8, tokens).
const (
	maxUndoEntries   = 64               // per client; the oldest is dropped
	maxUndoClients   = 64               // clients with a history; the stalest is dropped
	undoMergeWindow  = time.Second      // stepper taps within it are one entry
	recentLowerAsk   = 30 * time.Second // undo.raiseAfterLowerWindow
	undoRaiseAskDB   = 6.0              // SR-13: an undo raising more than this asks
	undoGainBoundDB  = gainBoundDB      // SR-31: an undo moving Gain more than this asks
	recentLowerSlack = time.Millisecond // the summary changes just after the 30 s
	// undoSignalDelay coalesces the signals of history changes: a burst of
	// writes wakes the dispatcher at most this often (Apply stays cheap);
	// the write's own echo usually delivers the new entry sooner.
	undoSignalDelay = 50 * time.Millisecond
)

// UndoKind is what an entry changed.
type UndoKind uint8

// Undo kinds.
const (
	UndoLevel  UndoKind = iota + 1 // fader, gain, sendLevel (dB)
	UndoSwitch                     // mute, sendOn
	UndoPan                        // sendPan
)

func (k UndoKind) String() string {
	return [...]string{"", "level", "switch", "pan"}[min(int(k), 3)]
}

// Directions of a change (the Undo label's arrow).
const (
	DirectionUp   = "up"
	DirectionDown = "down"
	DirectionNone = "none"
)

// What an undo asks before it writes (state undo asks), in this order.
const (
	AskUnmute                = "unmute"
	AskRaiseOver6dB          = "raiseOver6dB"
	AskRaiseAfterRecentLower = "raiseAfterRecentLower"
	AskConflict              = "conflict"
	AskBeforeSceneRecall     = "beforeSceneRecall"
	AskGainOver6dB           = "gainOver6dB"
)

// SceneMark names the scene recall that cut the history (CC-10).
type SceneMark struct {
	Number int    // 0: unknown
	Name   string // empty: unknown
}

// UndoSummary is a client's newest entry as the state reports it.
type UndoSummary struct {
	ClientID  string
	Param     model.ParamRef
	Kind      UndoKind
	From, To  model.Value
	Delta     float64 // To - From (dB or pan units); valid with HasDelta
	HasDelta  bool
	Direction string
	Asks      []string
	Scene     *SceneMark // the recall after the entry, if any
}

type undoEntry struct {
	param    model.ParamRef
	kind     UndoKind
	from, to model.Value
	at       time.Time
	merge    bool // a non-typed set outside a hold: the next tap may merge into it
	cut      *SceneMark
	// foreign: the console reported a change of the parameter since, not
	// by this client (the desk, a recall, another client), or the state was
	// read again (a reconnect): only then can it be a conflict.
	foreign bool
}

// undoHistory is every client's history. Guarded by Supervisor.mu.
type undoHistory struct {
	byClient map[string][]undoEntry
	expiry   *time.Timer // signals when a raiseAfterRecentLower ask runs out
	notify   *time.Timer // a pending coalesced signal of a history change
}

func newUndoHistory() undoHistory { return undoHistory{byClient: map[string][]undoEntry{}} }

// undoKindOf is the kind an entry of field f has; 0: f makes no entry.
func undoKindOf(f model.Field) UndoKind {
	switch f {
	case model.FieldFader, model.FieldGain, model.FieldSendLevel:
		return UndoLevel
	case model.FieldMute, model.FieldSendOn:
		return UndoSwitch
	case model.FieldSendPan:
		return UndoPan
	}
	return 0
}

// add appends an entry for client, merging stepper taps (merge) into the
// newest entry of the same parameter less than undoMergeWindow old.
func (h *undoHistory) add(client string, e undoEntry) {
	list := h.byClient[client]
	if n := len(list); n > 0 && e.merge && list[n-1].merge && list[n-1].param == e.param && e.at.Sub(list[n-1].at) < undoMergeWindow {
		last := list[n-1]
		last.to, last.at = e.to, e.at
		list = append(slices.Clone(list[:n-1]), last)
		h.byClient[client] = list
		return
	}
	if _, known := h.byClient[client]; !known && len(h.byClient) >= maxUndoClients {
		h.dropStalest()
	}
	list = append(slices.Clip(list), e)
	if len(list) > maxUndoEntries {
		list = slices.Clone(list[len(list)-maxUndoEntries:])
	}
	h.byClient[client] = list
}

// dropStalest forgets the client whose newest entry is the oldest.
func (h *undoHistory) dropStalest() {
	stalest, at := "", time.Time{}
	for c, list := range h.byClient {
		if t := list[len(list)-1].at; stalest == "" || t.Before(at) {
			stalest, at = c, t
		}
	}
	delete(h.byClient, stalest)
}

func (h *undoHistory) newest(client string) (undoEntry, bool) {
	list := h.byClient[client]
	if len(list) == 0 {
		return undoEntry{}, false
	}
	return list[len(list)-1], true
}

func (h *undoHistory) pop(client string) {
	list := h.byClient[client]
	if len(list) <= 1 {
		delete(h.byClient, client)
		return
	}
	h.byClient[client] = slices.Clone(list[:len(list)-1])
}

// cut marks every entry made before a scene recall at the console (the
// first recall after an entry names it).
func (h *undoHistory) cut(mark SceneMark) {
	for c, list := range h.byClient {
		out := slices.Clone(list)
		for i := range out {
			if out[i].cut == nil {
				m := mark
				out[i].cut = &m
			}
		}
		h.byClient[c] = out
	}
}

// markForeign notes a change of p reported by origin o: it is foreign to
// every client's entries on p except o's own (its echo).
func (h *undoHistory) markForeign(p model.ParamRef, o driver.Origin) {
	for c, list := range h.byClient {
		if o.MayWrite() && o.ClientID == c {
			continue
		}
		h.mark(c, list, func(e undoEntry) bool { return e.param == p })
	}
}

// markAllForeign: the whole state was read again (a reconnect).
func (h *undoHistory) markAllForeign() {
	for c, list := range h.byClient {
		h.mark(c, list, func(undoEntry) bool { return true })
	}
}

func (h *undoHistory) mark(client string, list []undoEntry, match func(undoEntry) bool) {
	if !slices.ContainsFunc(list, func(e undoEntry) bool { return match(e) && !e.foreign }) {
		return
	}
	out := slices.Clone(list)
	for i := range out {
		if match(out[i]) {
			out[i].foreign = true
		}
	}
	h.byClient[client] = out
}

// sceneMarkOf names a recall by what the driver knew of it.
func sceneMarkOf(e driver.SceneRecalled) SceneMark {
	return SceneMark{Number: max(e.LoadedScene, 0), Name: e.SceneName}
}

// recordLocked adds an entry for a change of p by origin from before to
// after; a change of nothing is not an entry. Callers hold mu.
func (s *Supervisor) recordLocked(o driver.Origin, p model.ParamRef, before, after model.Value, merge bool) {
	s.recordEntryLocked(o, p, before, after, merge, false)
}

// recordEntryLocked is recordLocked for an entry that may already have
// met a change elsewhere (foreign: its hold was ended without a release).
func (s *Supervisor) recordEntryLocked(o driver.Origin, p model.ParamRef, before, after model.Value, merge, foreign bool) {
	kind := undoKindOf(p.Field)
	if kind == 0 || !o.MayWrite() || s.sameValueLocked(p, before, after) {
		return
	}
	now := time.Now()
	s.undo.add(o.ClientID, undoEntry{param: p, kind: kind, from: before, to: after, at: now, merge: merge, foreign: foreign})
	if kind == UndoLevel && dbOf(after) < dbOf(before) { // a lowering: its ask runs out after 30 s
		s.armUndoExpiryLocked(recentLowerAsk + recentLowerSlack)
	}
	s.undoChangedLocked()
}

// undoChangedLocked signals the dispatcher once, undoSignalDelay after the
// first history change since the last such signal. Callers hold mu.
func (s *Supervisor) undoChangedLocked() {
	if s.undo.notify != nil {
		return // the pending signal covers this change too
	}
	s.undo.notify = time.AfterFunc(undoSignalDelay, func() {
		s.mu.Lock()
		s.undo.notify = nil
		s.mu.Unlock()
		s.signal()
	})
}

// armUndoExpiryLocked signals the dispatcher after d, so the state's undo
// asks are sent again once raiseAfterRecentLower runs out.
func (s *Supervisor) armUndoExpiryLocked(d time.Duration) {
	if s.undo.expiry == nil {
		s.undo.expiry = time.AfterFunc(d, s.signal)
		return
	}
	s.undo.expiry.Reset(d)
}

// sameValueLocked reports whether a and b are one console value of p:
// levels by the parameter's grid (Gain within half a step), others exactly.
func (s *Supervisor) sameValueLocked(p model.ParamRef, a, b model.Value) bool {
	if a.Kind() != model.KindLevel || b.Kind() != model.KindLevel {
		return a.Equal(b)
	}
	x, y := dbOf(a), dbOf(b)
	if math.IsInf(x, -1) || math.IsInf(y, -1) {
		return math.IsInf(x, -1) && math.IsInf(y, -1)
	}
	if p.Field == model.FieldGain {
		return math.Abs(x-y) <= s.halfStepDB(p.Strip.Index)+1e-9
	}
	return s.sameLevelLocked(p, x, y)
}

// undoSummariesLocked lists every client's newest entry with its asks, by
// client id. Callers hold mu.
func (s *Supervisor) undoSummariesLocked() []UndoSummary {
	now := time.Now()
	var out []UndoSummary
	for _, client := range slices.Sorted(maps.Keys(s.undo.byClient)) {
		e, _ := s.undo.newest(client)
		out = append(out, s.summaryLocked(client, e, now))
	}
	return out
}

func (s *Supervisor) summaryLocked(client string, e undoEntry, now time.Time) UndoSummary {
	sum := UndoSummary{ClientID: client, Param: e.param, Kind: e.kind, From: e.from, To: e.to, Direction: DirectionNone,
		Asks: s.undoAsksLocked(e, now), Scene: e.cut}
	if e.kind == UndoSwitch {
		return sum
	}
	if f, t := numberOf(e.from), numberOf(e.to); !math.IsInf(f, 0) && !math.IsInf(t, 0) {
		sum.Delta, sum.HasDelta = t-f, true
	}
	if e.kind == UndoLevel {
		switch f, t := dbOf(e.from), dbOf(e.to); {
		case t > f:
			sum.Direction = DirectionUp
		case t < f:
			sum.Direction = DirectionDown
		}
	}
	return sum
}

// numberOf is a level in dB or a pan position.
func numberOf(v model.Value) float64 {
	if p, ok := v.Pan(); ok {
		return p
	}
	return dbOf(v)
}
