// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package analyze builds knowledge from decoded traffic: the latest value of
// every key, which keys changed after an operator's mark (candidates for that
// label), snapshots and their differences, and the initial-sync harvest after
// an app connects. It is a pure function of the observations and marks fed
// to it, driven by their timestamps (never the wall clock), so a recorded
// session re-analyzes to the same result.
//
// An Analyzer is owned by one goroutine; it is not safe for concurrent use.
package analyze

import (
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
)

// Defaults for Config.
const (
	DefaultMarkWindow   = 5 * time.Second
	DefaultHarvestQuiet = 2 * time.Second
	DefaultNoiseChanges = 2
	maxDistinctValues   = 16
	changeHistory       = 32
)

// Config tunes the analyzer.
type Config struct {
	// MarkWindow is how long after a mark changes are attributed to it.
	MarkWindow time.Duration
	// HarvestQuiet ends an initial-sync harvest: no new key for this long.
	HarvestQuiet time.Duration
	// NoiseChanges flags a candidate as noise when its key changed this many
	// times in the MarkWindow before the mark (meters, clocks, keep-alives).
	NoiseChanges int
	// Notify receives live events (nil: none). Called on the owner goroutine.
	Notify func(Event)
}

func (c Config) withDefaults() Config {
	if c.MarkWindow <= 0 {
		c.MarkWindow = DefaultMarkWindow
	}
	if c.HarvestQuiet <= 0 {
		c.HarvestQuiet = DefaultHarvestQuiet
	}
	if c.NoiseChanges <= 0 {
		c.NoiseChanges = DefaultNoiseChanges
	}
	if c.Notify == nil {
		c.Notify = func(Event) {}
	}
	return c
}

// keyState is everything known about one key.
type keyState struct {
	key       decode.Key
	decoder   string
	first     decode.Value
	last      decode.Value
	firstAt   time.Time
	lastAt    time.Time
	firstEv   flow.Evidence
	changeEv  flow.Evidence // first change after the first value
	count     int
	changes   int
	dirs      [3]bool // indexed by flow.Dir
	values    []string
	moreVals  bool
	changedAt []time.Time // the last changeHistory change times
	varying   map[int]bool
}

// Analyzer accumulates observations, marks and snapshots.
type Analyzer struct {
	cfg       Config
	keys      map[decode.Key]*keyState
	order     []decode.Key
	marks     []*Mark
	snaps     []Snapshot
	harvests  []*Harvest
	apps      map[netip.AddrPort]bool
	readDirs  map[decode.Key][3]bool // directions of valueless messages for keys without a value yet
	lastAt    time.Time
	observed  int
	valueless int
}

// New returns an analyzer.
func New(cfg Config) *Analyzer {
	return &Analyzer{
		cfg: cfg.withDefaults(), keys: map[decode.Key]*keyState{}, apps: map[netip.AddrPort]bool{},
		readDirs: map[decode.Key][3]bool{},
	}
}

// Observe adds one observation. Observations must arrive in time order.
func (a *Analyzer) Observe(o flow.Observation) {
	a.observed++
	a.advance(o.At)
	a.noteApp(o)
	if !o.Msg.Value.Present {
		a.valueless++
		a.touchDir(o)
		return
	}
	prev, changed := a.update(o)
	if changed {
		a.attribute(o, prev)
	}
	a.harvestSee(o)
}

// Tick closes marks and harvests whose time is over (live sessions).
func (a *Analyzer) Tick(now time.Time) { a.advance(now) }

func (a *Analyzer) advance(now time.Time) {
	if now.After(a.lastAt) {
		a.lastAt = now
	}
	a.closeMarks(a.lastAt)
	a.closeHarvest(a.lastAt)
}

func (a *Analyzer) touchDir(o flow.Observation) {
	if st := a.keys[o.Msg.Key]; st != nil {
		st.dirs[o.Dir] = true
		return
	}
	d := a.readDirs[o.Msg.Key]
	d[o.Dir] = true
	a.readDirs[o.Msg.Key] = d
}

// update stores the value and reports the previous one and whether it changed.
func (a *Analyzer) update(o flow.Observation) (prev *decode.Value, changed bool) {
	v := o.Msg.Value
	st := a.keys[o.Msg.Key]
	if st == nil {
		st = &keyState{key: o.Msg.Key, decoder: o.Decoder, first: v, last: v, firstAt: o.At, firstEv: o.Evidence}
		a.keys[o.Msg.Key] = st
		a.order = append(a.order, o.Msg.Key)
		st.addValue(v.Text)
		st.dirs = a.readDirs[o.Msg.Key]
		delete(a.readDirs, o.Msg.Key)
		st.count, st.lastAt, st.dirs[o.Dir] = 1, o.At, true
		return nil, true
	}
	st.count++
	st.lastAt, st.dirs[o.Dir] = o.At, true
	if st.last.Equal(v) {
		return nil, false
	}
	old := st.last
	if st.changes == 0 {
		st.changeEv = o.Evidence
	}
	st.changes++
	st.changedAt = append(st.changedAt, o.At)
	if len(st.changedAt) > changeHistory {
		st.changedAt = st.changedAt[1:]
	}
	if o.Msg.Key.Kind == decode.KindBinary {
		st.markVarying(old.Text, v.Text)
	}
	st.last = v
	st.addValue(v.Text)
	return &old, true
}

func (st *keyState) addValue(s string) {
	if slices.Contains(st.values, s) {
		return
	}
	if len(st.values) >= maxDistinctValues {
		st.moreVals = true
		return
	}
	st.values = append(st.values, s)
}

// changesIn counts the key's changes in [from, to).
func (st *keyState) changesIn(from, to time.Time) int {
	n := 0
	for _, t := range st.changedAt {
		if !t.Before(from) && t.Before(to) {
			n++
		}
	}
	return n
}

func (a *Analyzer) noteApp(o flow.Observation) {
	if a.apps[o.App] {
		return
	}
	a.apps[o.App] = true
	a.harvestStart(o)
}

// Value returns the latest value of k.
func (a *Analyzer) Value(k decode.Key) (decode.Value, bool) {
	st := a.keys[k]
	if st == nil {
		return decode.Value{}, false
	}
	return st.last, true
}

// Keys returns every key with a value, in order of first appearance.
func (a *Analyzer) Keys() []decode.Key { return slices.Clone(a.order) }
