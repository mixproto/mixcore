// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package burst counts the distinct parameters pushed within a sliding
// time window, for the scene-recall heuristic of every family (x32.md
// X32-F60, xair.md section 7, wing.md section 7). Each push costs amortized
// O(1) and the memory is bounded by the number of distinct parameters,
// whatever the push rate (review sec M3).
package burst

import (
	"slices"
	"time"
)

type entry struct {
	idx int
	at  time.Time
}

// Window is not safe for concurrent use (one owner goroutine).
type Window struct {
	span  time.Duration
	last  map[int]time.Time // newest push per parameter within the span
	order []entry           // pushes in arrival order; may hold superseded ones
}

// New returns a window of span.
func New(span time.Duration) *Window {
	return &Window{span: span, last: map[int]time.Time{}}
}

// Add records a push of parameter idx at time at and returns how many
// distinct parameters were pushed within span up to at.
func (w *Window) Add(idx int, at time.Time) int {
	w.last[idx] = at
	w.order = append(w.order, entry{idx: idx, at: at})
	w.evict(at)
	if len(w.order) > 2*len(w.last)+16 {
		w.compact()
	}
	return len(w.last)
}

// evict drops pushes older than span; a parameter leaves the count when
// its newest push does.
func (w *Window) evict(now time.Time) {
	n := 0
	for ; n < len(w.order) && now.Sub(w.order[n].at) > w.span; n++ {
		e := w.order[n]
		if last, ok := w.last[e.idx]; ok && !last.After(e.at) {
			delete(w.last, e.idx)
		}
	}
	w.order = w.order[n:]
}

// compact keeps only each parameter's newest push, in arrival order, so
// the queue stays bounded by the distinct parameters.
func (w *Window) compact() {
	seen := make(map[int]bool, len(w.last))
	kept := make([]entry, 0, len(w.last))
	for i := len(w.order) - 1; i >= 0; i-- {
		if e := w.order[i]; !seen[e.idx] {
			seen[e.idx] = true
			kept = append(kept, e)
		}
	}
	slices.Reverse(kept)
	w.order = kept
}

// Len is the number of queued pushes (for tests).
func (w *Window) Len() int { return len(w.order) }

// Reset forgets everything.
func (w *Window) Reset() {
	clear(w.last)
	w.order = w.order[:0]
}
