// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package analyze

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
)

// KeyInfo summarizes one key for reports.
type KeyInfo struct {
	Key        decode.Key
	Decoder    string
	Dirs       string // "app->console", "console->app" or "both"
	Count      int
	Changes    int
	First      decode.Value
	Last       decode.Value
	FirstAt    time.Time
	Values     []string // distinct values in order of appearance
	MoreValues bool     // more than the values listed were seen
	FirstEv    flow.Evidence
	ChangeEv   flow.Evidence // zero when the value never changed
	Labels     []string      // labels of marks where it was a non-noisy candidate
	Varying    []int         // binary keys: frame offsets that changed
}

// Result is everything the analyzer knows.
type Result struct {
	Keys      []KeyInfo // in order of first appearance
	Marks     []*Mark
	Snapshots []Snapshot
	Diffs     []Diff // each snapshot against the previous one
	Harvests  []*Harvest
	Observed  int
	Valueless int // messages without a value (read requests)
}

// Result summarizes the session so far.
func (a *Analyzer) Result() Result {
	labels := map[decode.Key][]string{}
	for _, m := range a.marks {
		for _, c := range m.Candidates {
			if !c.Noisy {
				labels[c.Key] = append(labels[c.Key], m.Label)
			}
		}
	}
	r := Result{Marks: a.Marks(), Snapshots: a.Snapshots(), Harvests: a.Harvests(),
		Observed: a.observed, Valueless: a.valueless}
	for _, k := range a.order {
		r.Keys = append(r.Keys, a.keyInfo(a.keys[k], labels[k]))
	}
	for i := 1; i < len(a.snaps); i++ {
		r.Diffs = append(r.Diffs, a.Diff(a.snaps[i-1], a.snaps[i]))
	}
	return r
}

func (a *Analyzer) keyInfo(st *keyState, labels []string) KeyInfo {
	ki := KeyInfo{
		Key: st.key, Decoder: st.decoder, Count: st.count, Changes: st.changes, First: st.first, Last: st.last,
		FirstAt: st.firstAt, Values: slices.Clone(st.values), MoreValues: st.moreVals, FirstEv: st.firstEv,
		ChangeEv: st.changeEv, Labels: slices.Compact(labels),
		Varying: slices.Sorted(maps.Keys(st.varying)),
	}
	switch {
	case st.dirs[flow.ToConsole] && st.dirs[flow.FromConsole]:
		ki.Dirs = "both"
	case st.dirs[flow.ToConsole]:
		ki.Dirs = flow.ToConsole.String()
	default:
		ki.Dirs = flow.FromConsole.String()
	}
	return ki
}

// Point is one known (wire value, engineering value) pair for a law.
type Point struct {
	X, Y  float64
	Label string
}

// LawPoints returns, for key k, a point per closed mark with a parsed value:
// X is the key's value at the end of the mark's window when it changed
// there, otherwise its value at the mark. Marks where k had no number are
// skipped.
func (a *Analyzer) LawPoints(k decode.Key) []Point {
	var out []Point
	for _, m := range a.marks {
		if !m.Closed || !m.HasPoint {
			continue
		}
		x, ok := m.numericAt[k]
		for _, c := range m.Candidates {
			if c.Key == k && c.To.HasNum {
				x, ok = c.To.Num, true
			}
		}
		if ok {
			out = append(out, Point{X: x, Y: m.Point, Label: m.Label})
		}
	}
	return out
}

// FindKey resolves a key from its displayed form (Key.String) or its ID.
func (a *Analyzer) FindKey(s string) (decode.Key, bool) {
	s = strings.TrimSpace(s)
	for _, k := range a.order {
		if k.String() == s || k.ID == s {
			return k, true
		}
	}
	return decode.Key{}, false
}
