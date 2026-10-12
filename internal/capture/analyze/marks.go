// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package analyze

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
)

// Candidate is a key that changed after a mark.
type Candidate struct {
	Key      decode.Key
	From     decode.Value // value at the mark; not Present for a key first seen after it
	To       decode.Value // last value inside the window
	Changes  int
	FirstAt  time.Time
	Dir      flow.Dir // direction of the first change
	Noisy    bool     // the key also changed often before the mark
	Evidence flow.Evidence
}

// Change renders "from→to" (binary keys: the changed byte offsets).
func (c Candidate) Change() string { return describeChange(c.Key, c.From, c.To) }

// Mark is an operator's label for what they are about to do (or just did).
type Mark struct {
	ID         int
	Label      string
	At         time.Time
	Closed     bool
	Candidates []Candidate // sorted when closed: real ones first, then by time
	// Point is the engineering value parsed from a label ending in "= <n>"
	// (e.g. "ch1 fader = -10 dB"); HasPoint is false without one.
	Point    float64
	HasPoint bool
	// numericAt is every numeric value at mark time (law points for keys
	// set before the mark).
	numericAt map[decode.Key]float64
	index     map[decode.Key]int
}

// Mark adds a mark at time at and returns it. A new mark closes the open one,
// so changes are attributed to the latest label.
func (a *Analyzer) Mark(at time.Time, label string) *Mark {
	a.advance(at)
	for _, m := range a.marks {
		a.closeMark(m)
	}
	m := &Mark{ID: len(a.marks) + 1, Label: label, At: at, index: map[decode.Key]int{}}
	m.Point, m.HasPoint = ParsePoint(label)
	m.numericAt = map[decode.Key]float64{}
	for k, st := range a.keys {
		if st.last.HasNum {
			m.numericAt[k] = st.last.Num
		}
	}
	a.marks = append(a.marks, m)
	return m
}

// ParsePoint reads the value after the last '=' of a label: a number or
// -inf / -oo, optionally followed by a unit ("= -10 dB", "=-inf").
func ParsePoint(label string) (float64, bool) {
	i := strings.LastIndexByte(label, '=')
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(label[i+1:])
	if len(fields) == 0 {
		return 0, false
	}
	switch s := strings.ToLower(fields[0]); s {
	case "-inf", "-oo", "off":
		return math.Inf(-1), true
	default:
		f, err := strconv.ParseFloat(strings.TrimPrefix(s, "+"), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
}

// attribute records a change of o's key for the open mark it falls into.
func (a *Analyzer) attribute(o flow.Observation, prev *decode.Value) {
	m := a.openMark(o.At)
	if m == nil {
		return
	}
	if i, ok := m.index[o.Msg.Key]; ok {
		c := &m.Candidates[i]
		c.To, c.Changes = o.Msg.Value, c.Changes+1
		return
	}
	c := Candidate{Key: o.Msg.Key, To: o.Msg.Value, Changes: 1, FirstAt: o.At, Dir: o.Dir, Evidence: o.Evidence}
	if prev != nil {
		c.From = *prev
	}
	st := a.keys[o.Msg.Key]
	// The change being attributed is already in the history; count only earlier ones.
	c.Noisy = st.changesIn(m.At.Add(-a.cfg.MarkWindow), m.At) >= a.cfg.NoiseChanges
	m.index[o.Msg.Key] = len(m.Candidates)
	m.Candidates = append(m.Candidates, c)
	a.cfg.Notify(CandidateFound{Mark: m, Candidate: c})
}

func (a *Analyzer) openMark(at time.Time) *Mark {
	if len(a.marks) == 0 {
		return nil
	}
	m := a.marks[len(a.marks)-1]
	if m.Closed || at.Before(m.At) || at.After(m.At.Add(a.cfg.MarkWindow)) {
		return nil
	}
	return m
}

func (a *Analyzer) closeMarks(now time.Time) {
	for _, m := range a.marks {
		if !m.Closed && now.After(m.At.Add(a.cfg.MarkWindow)) {
			a.closeMark(m)
		}
	}
}

func (a *Analyzer) closeMark(m *Mark) {
	if m.Closed {
		return
	}
	m.Closed = true
	slices.SortStableFunc(m.Candidates, func(x, y Candidate) int {
		if x.Noisy != y.Noisy {
			if x.Noisy {
				return 1
			}
			return -1
		}
		return x.FirstAt.Compare(y.FirstAt)
	})
	m.index = nil
	a.cfg.Notify(MarkClosed{Mark: m})
}

// Marks returns the marks so far (closed ones are final).
func (a *Analyzer) Marks() []*Mark { return slices.Clone(a.marks) }
