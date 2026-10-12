// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/mixproto/mixcore/internal/capture/analyze"
	"github.com/mixproto/mixcore/internal/capture/law"
	"github.com/mixproto/mixcore/internal/capture/session"
)

// reanalyze replays a recorded session (capture plus the marks and
// snapshots of its index) into a fresh engine that writes no index.
func reanalyze(dir string, af *analysisFlags, out io.Writer) (*session.Dir, session.Meta, *engine, error) {
	d, err := session.Open(dir)
	if err != nil {
		return nil, session.Meta{}, nil, err
	}
	fail := func(err error) (*session.Dir, session.Meta, *engine, error) {
		return nil, session.Meta{}, nil, errors.Join(err, d.Close())
	}
	meta, err := d.ReadMeta()
	if err != nil {
		return fail(err)
	}
	console, err := parseConsoleIP(meta.Console)
	if err != nil {
		return fail(err)
	}
	cfg, err := af.flowConfig(console)
	if err != nil {
		return fail(err)
	}
	if len(cfg.Ports) == 0 {
		cfg.Ports = meta.Ports // the ports the recording analyzed
	}
	e, err := newEngine(af, cfg, nil, out)
	if err != nil {
		return fail(err)
	}
	ann, err := readAnnotations(d)
	if err != nil {
		return fail(err)
	}
	f, err := d.OpenFile(meta.Capture)
	if err != nil {
		return fail(err)
	}
	err = errors.Join(replay(e, f, meta.Capture, ann), f.Close(), e.finish())
	if err != nil {
		return fail(err)
	}
	return d, meta, e, nil
}

func readAnnotations(d *session.Dir) ([]session.Record, error) {
	f, err := d.OpenFile(session.FileIndex)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return session.Annotations(f)
}

func runReport(_ context.Context, args []string, _ io.Reader, out io.Writer) error {
	var af analysisFlags
	fs := newFlagSet("report", out)
	dir := fs.String("session", "", "session directory (required)")
	af.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("%w: -session is required", errUsage)
	}
	d, meta, e, err := reanalyze(*dir, &af, io.Discard)
	if err != nil {
		return err
	}
	e.out = out
	return errors.Join(e.writeReport(d, meta), d.Close())
}

// pointList collects repeated -p x=y flags.
type pointList []law.Point

func (p *pointList) String() string { return fmt.Sprint(*p) }

func (p *pointList) Set(s string) error {
	xs, ys, ok := strings.Cut(s, "=")
	x, errX := strconv.ParseFloat(strings.TrimSpace(xs), 64)
	y, okY := analyze.ParsePoint("=" + ys)
	if !ok || errX != nil || !okY {
		return fmt.Errorf("want wire=engineering, e.g. 0.75=0 or 0=-inf, got %q", s)
	}
	*p = append(*p, law.Point{X: x, Y: y})
	return nil
}

func runLaw(_ context.Context, args []string, _ io.Reader, out io.Writer) error {
	var af analysisFlags
	var extra pointList
	fs := newFlagSet("law", out)
	dir := fs.String("session", "", "session directory (optional with -p)")
	keyText := fs.String("key", "", "key as shown in the report, e.g. /ch/01/mix/fader or \"NRPN ch1 0x0017\"")
	tol := fs.Float64("tol", law.DefaultTolerance, "largest residual a law may have, in engineering units")
	fs.Var(&extra, "p", "extra point wire=engineering (repeatable), e.g. -p 0.75=0 -p 0=-inf")
	af.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	pts := []law.Point(extra)
	if *dir != "" {
		got, err := sessionLawPoints(*dir, &af, *keyText)
		if err != nil {
			return err
		}
		pts = append(got, pts...)
	}
	for _, p := range pts {
		fmt.Fprintf(out, "point x=%.6g y=%s\n", p.X, formatY(p.Y))
	}
	best, all, err := law.Fit(pts, *tol)
	if err != nil {
		return fmt.Errorf("law: %w", err)
	}
	fmt.Fprintf(out, "best: %s\n", best)
	for _, l := range all {
		fmt.Fprintf(out, "  %s\n", l)
	}
	return nil
}

func sessionLawPoints(dir string, af *analysisFlags, keyText string) ([]law.Point, error) {
	d, _, e, err := reanalyze(dir, af, io.Discard)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	k, ok := e.an.FindKey(keyText)
	if !ok {
		return nil, fmt.Errorf("%w: no key %q in the session (see keys.csv)", errUsage, keyText)
	}
	var pts []law.Point
	for _, p := range e.an.LawPoints(k) {
		pts = append(pts, law.Point{X: p.X, Y: p.Y})
	}
	return pts, nil
}

func formatY(y float64) string {
	if math.IsInf(y, -1) {
		return "-inf"
	}
	return strconv.FormatFloat(y, 'g', -1, 64)
}
