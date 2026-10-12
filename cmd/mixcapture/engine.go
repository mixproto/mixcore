// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mixproto/mixcore/internal/capture/analyze"
	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/law"
	"github.com/mixproto/mixcore/internal/capture/report"
	"github.com/mixproto/mixcore/internal/capture/session"
)

// maxLiveCandidates is how many candidates a closed mark prints.
const maxLiveCandidates = 8

// engine owns the flow table, the analyzer and the index of one session. It
// is used by one goroutine at a time.
type engine struct {
	table   *flow.Table
	an      *analyze.Analyzer
	idx     *session.IndexWriter // nil when re-analyzing a recorded session
	out     io.Writer
	started time.Time
	err     error // first index write error
}

func newEngine(af *analysisFlags, cfg flow.Config, idx *session.IndexWriter, out io.Writer) (*engine, error) {
	tab, err := flow.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}
	e := &engine{table: tab, idx: idx, out: out}
	e.an = analyze.New(af.analyzeConfig(e.notify))
	return e, nil
}

func (e *engine) frame(fr flow.Frame) {
	if e.started.IsZero() {
		e.started = fr.At
	}
	e.table.Add(fr, e.observe)
}

func (e *engine) observe(o flow.Observation) {
	if e.idx != nil && e.err == nil {
		e.err = e.idx.Message(o)
	}
	e.an.Observe(o)
}

func (e *engine) tick(now time.Time) {
	e.table.Tick(now, e.observe)
	e.an.Tick(now)
}

// mark adds a mark. Flows still detecting their decoder are decided first,
// so the state at the mark is complete (live and replay alike).
func (e *engine) mark(at time.Time, label string) {
	e.table.Flush(e.observe)
	m := e.an.Mark(at, label)
	if e.idx != nil && e.err == nil {
		e.err = e.idx.Mark(m.ID, at, label)
	}
	pt := ""
	if m.HasPoint {
		pt = fmt.Sprintf(" (law point %g)", m.Point)
	}
	fmt.Fprintf(e.out, "mark %d %q%s: watching changes for the next seconds\n", m.ID, label, pt)
}

func (e *engine) snapshot(at time.Time, label string) {
	e.table.Flush(e.observe)
	s := e.an.Snapshot(at, label)
	if e.idx != nil && e.err == nil {
		e.err = e.idx.Snapshot(s.ID, at, label)
	}
	fmt.Fprintf(e.out, "snapshot %d %q: %d keys\n", s.ID, label, len(s.Values))
	if snaps := e.an.Snapshots(); len(snaps) > 1 {
		e.printDiff(e.an.Diff(snaps[len(snaps)-2], snaps[len(snaps)-1]))
	}
}

func (e *engine) finish() error {
	e.table.Flush(e.observe)
	e.an.Finish()
	if e.idx != nil {
		e.err = errors.Join(e.err, e.idx.Flush())
	}
	return e.err
}

// notify prints live analyzer events.
func (e *engine) notify(ev analyze.Event) {
	switch x := ev.(type) {
	case analyze.CandidateFound:
		if !x.Candidate.Noisy {
			fmt.Fprintf(e.out, "  candidate: %s\n", x.Candidate.Change())
		}
	case analyze.MarkClosed:
		e.printMark(x.Mark)
	case analyze.HarvestDone:
		h := x.Harvest
		fmt.Fprintf(e.out, "harvest %d: initial sync from %s ended, %d keys with values (harvest.csv)\n",
			h.ID, h.App, len(h.Keys))
	}
}

func (e *engine) printMark(m *analyze.Mark) {
	noisy := 0
	for _, c := range m.Candidates {
		if c.Noisy {
			noisy++
		}
	}
	kept := len(m.Candidates) - noisy // closed marks sort noise last
	fmt.Fprintf(e.out, "mark %d %q closed: %d candidate(s), %d noisy key(s) ignored\n", m.ID, m.Label, kept, noisy)
	for i, c := range m.Candidates[:kept] {
		if i == maxLiveCandidates {
			fmt.Fprintf(e.out, "    ... %d more in the report\n", kept-i)
			break
		}
		fmt.Fprintf(e.out, "    %d. %s  (%s, %d change(s))\n", i+1, c.Change(), c.Dir, c.Changes)
	}
}

func (e *engine) printDiff(d analyze.Diff) {
	fmt.Fprintf(e.out, "diff snapshot %d -> %d: %d change(s), %d streaming key(s) ignored\n",
		d.From, d.To, len(d.Changes), len(d.Noise))
	for i, c := range d.Changes {
		if i == maxLiveCandidates*4 {
			fmt.Fprintf(e.out, "    ... %d more in the report\n", len(d.Changes)-i)
			return
		}
		fmt.Fprintf(e.out, "    %s\n", c.Detail())
	}
}

// laws fits a law for every key with at least two labelled points.
func (e *engine) laws() map[decode.Key]report.Fit {
	out := map[decode.Key]report.Fit{}
	for _, k := range e.an.Keys() {
		pts := e.an.LawPoints(k)
		if len(pts) < 2 {
			continue
		}
		if l, err := fitPoints(pts); err == nil {
			out[k] = report.Fit{Law: l, Points: pts}
		}
	}
	return out
}

func fitPoints(pts []analyze.Point) (law.Law, error) {
	lp := make([]law.Point, 0, len(pts))
	for _, p := range pts {
		lp = append(lp, law.Point{X: p.X, Y: p.Y})
	}
	l, _, err := law.Fit(lp, law.DefaultTolerance)
	return l, err
}

// writeReport writes the report files of the session.
func (e *engine) writeReport(d *session.Dir, meta session.Meta) error {
	if meta.Started.IsZero() {
		meta.Started = e.started
	}
	in := report.Input{Meta: meta, Result: e.an.Result(), Flows: e.table.Flows(), Stats: e.table.Stats(), Laws: e.laws()}
	if err := report.Write(d, in); err != nil {
		return err
	}
	r := in.Result
	fmt.Fprintf(e.out, "report: %d keys, %d marks, %d snapshots, %d harvest(s), %d law(s) -> %s/%s\n",
		len(r.Keys), len(r.Marks), len(r.Snapshots), len(r.Harvests), len(in.Laws), d.Path(), report.FileReport)
	return nil
}
