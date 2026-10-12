// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package analyze

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
)

// Snapshot is the latest value of every key at one moment.
type Snapshot struct {
	ID     int
	Label  string
	At     time.Time
	Values map[decode.Key]decode.Value
}

// Change is one difference between two snapshots.
type Change struct {
	Key   decode.Key
	From  decode.Value // not Present when the key was unknown at the first snapshot
	To    decode.Value
	Added bool
}

// Detail renders the change like a mark candidate.
func (c Change) Detail() string { return describeChange(c.Key, c.From, c.To) }

// Diff is the difference between two snapshots. Streaming keys (meters,
// clocks: they change all the time) are listed apart in Noise.
type Diff struct {
	From, To int // snapshot IDs
	Changes  []Change
	Noise    []Change
}

// Streaming thresholds: a key that changed at least streamMinChanges times,
// on average more than streamRate times a second over its life, is a stream.
const (
	streamMinChanges = 20
	streamRate       = 2.0
)

// Diff compares two snapshots and moves streaming keys to Noise.
func (a *Analyzer) Diff(from, to Snapshot) Diff {
	d := DiffSnapshots(from, to)
	kept := d.Changes[:0]
	for _, c := range d.Changes {
		if a.isStreaming(c.Key) {
			d.Noise = append(d.Noise, c)
			continue
		}
		kept = append(kept, c)
	}
	d.Changes = kept
	return d
}

func (a *Analyzer) isStreaming(k decode.Key) bool {
	st := a.keys[k]
	if st == nil || st.changes < streamMinChanges {
		return false
	}
	life := st.lastAt.Sub(st.firstAt).Seconds()
	return life <= 0 || float64(st.changes)/life > streamRate
}

// Snapshot records the current state.
func (a *Analyzer) Snapshot(at time.Time, label string) Snapshot {
	a.advance(at)
	vals := make(map[decode.Key]decode.Value, len(a.keys))
	for k, st := range a.keys {
		vals[k] = st.last
	}
	s := Snapshot{ID: len(a.snaps) + 1, Label: label, At: at, Values: vals}
	a.snaps = append(a.snaps, s)
	return s
}

// Snapshots returns the snapshots so far.
func (a *Analyzer) Snapshots() []Snapshot { return slices.Clone(a.snaps) }

// DiffSnapshots compares two snapshots: changed and added keys, sorted by key.
func DiffSnapshots(from, to Snapshot) Diff {
	d := Diff{From: from.ID, To: to.ID}
	for _, k := range slices.SortedFunc(maps.Keys(to.Values), compareKeys) {
		nv := to.Values[k]
		ov, had := from.Values[k]
		switch {
		case !had:
			d.Changes = append(d.Changes, Change{Key: k, To: nv, Added: true})
		case !ov.Equal(nv):
			d.Changes = append(d.Changes, Change{Key: k, From: ov, To: nv})
		}
	}
	return d
}

func compareKeys(x, y decode.Key) int {
	return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.ID, y.ID))
}
