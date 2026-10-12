// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package report writes what a session found as tables a protocol researcher
// pastes into docs/protocols/<family>.md as [CAPTURE] facts: report.md (marks
// with candidates, laws, snapshot diffs, harvest summary, labelled keys) and
// keys.csv, harvest.csv, marks.csv with every row.
package report

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/analyze"
	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/law"
	"github.com/mixproto/mixcore/internal/capture/session"
)

// Output file names.
const (
	FileReport  = "report.md"
	FileKeys    = "keys.csv"
	FileHarvest = "harvest.csv"
	FileMarks   = "marks.csv"
	tagCapture  = "[CAPTURE]"
	maxShownMD  = 12 // candidates per mark, values per key in markdown
)

// Fit is a fitted law with the points it came from.
type Fit struct {
	Law    law.Law
	Points []analyze.Point
}

// Input is everything a report shows.
type Input struct {
	Meta   session.Meta
	Result analyze.Result
	Flows  []flow.FlowInfo
	Stats  flow.Stats
	Laws   map[decode.Key]Fit
}

// Write writes report.md and the CSV files into d.
func Write(d *session.Dir, in Input) error {
	keys, harvest, marks, err := csvFiles(in)
	if err != nil {
		return err
	}
	return errors.Join(
		d.WriteFile(FileReport, []byte(Markdown(in))),
		d.WriteFile(FileKeys, keys),
		d.WriteFile(FileHarvest, harvest),
		d.WriteFile(FileMarks, marks),
	)
}

// Markdown renders report.md.
func Markdown(in Input) string {
	var b strings.Builder
	m := in.Meta
	fmt.Fprintf(&b, "# mixcapture report: %s\n\n", m.Console)
	fmt.Fprintf(&b, "> **Privacy.** %s\n\n", session.PrivacyNote)
	fmt.Fprintf(&b, "Mode %s, started %s, capture `%s`. Evidence cells are `file:byte offset` of the packet "+
		"record in that file (open it in Wireshark and use Go to Packet, or `pkt` in index.jsonl). Facts below are "+
		"candidates: paste confirmed rows into `docs/protocols/<family>.md` tagged %s with the session path.\n\n",
		m.Mode, m.Started.Format(time.RFC3339), m.Capture, tagCapture)
	writeFlows(&b, in)
	writeMarks(&b, in)
	writeLaws(&b, in)
	writeDiffs(&b, in)
	writeHarvests(&b, in)
	writeKeys(&b, in)
	return b.String()
}

func writeFlows(b *strings.Builder, in Input) {
	st := in.Stats
	fmt.Fprintf(b, "## Traffic\n\n%d frames: %d decoded flows below, %d not to or from the console, %d not UDP/TCP, "+
		"%d malformed, %d TCP gaps. %d messages, %d without a value (reads).\n\n",
		st.Frames, len(in.Flows), st.Foreign, st.Skipped, st.Malformed, st.Gaps, in.Result.Observed, in.Result.Valueless)
	if st.Dropped > 0 {
		fmt.Fprintf(b, "**%d decoded messages dropped as too large or too deep** (JSON objects over 1 MiB or 32 levels); "+
			"their bytes are in the capture file.\n\n", st.Dropped)
	}
	for _, f := range in.Flows {
		fmt.Fprintf(b, "- %s\n", f)
	}
	b.WriteString("\n")
}

func writeMarks(b *strings.Builder, in Input) {
	b.WriteString("## Marks\n\n")
	if len(in.Result.Marks) == 0 {
		b.WriteString("No marks.\n\n")
		return
	}
	for _, mk := range in.Result.Marks {
		fmt.Fprintf(b, "### Mark %d: %q at %s\n\n", mk.ID, mk.Label, rel(in, mk.At))
		var real []analyze.Candidate
		var noise []string
		for _, c := range mk.Candidates {
			if c.Noisy {
				noise = append(noise, "`"+cell(c.Key.String())+"`")
				continue
			}
			real = append(real, c)
		}
		writeCandidates(b, real)
		if len(noise) > 0 {
			fmt.Fprintf(b, "Ignored as noise (they also changed before the mark): %s.\n\n", strings.Join(noise, ", "))
		}
	}
}

func writeCandidates(b *strings.Builder, cands []analyze.Candidate) {
	if len(cands) == 0 {
		b.WriteString("No key changed in the window apart from noise.\n\n")
		return
	}
	b.WriteString("| Candidate | Change | Direction | Changes | Evidence |\n|---|---|---|---|---|\n")
	for i, c := range cands {
		if i == maxShownMD {
			fmt.Fprintf(b, "| ... %d more in marks.csv | | | | |\n", len(cands)-i)
			break
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %d | %s |\n", cell(c.Key.String()), short(cell(changeOnly(c.Change(), c.Key))),
			c.Dir, c.Changes, c.Evidence)
	}
	b.WriteString("\n")
}

// maxCell bounds a value cell in markdown; the CSV files keep full values.
const maxCell = 80

func short(s string) string {
	r := []rune(s)
	if len(r) <= maxCell {
		return s
	}
	return string(r[:maxCell]) + "..."
}

// changeOnly strips the key from a rendered change.
func changeOnly(s string, k decode.Key) string { return strings.TrimPrefix(s, k.String()+" ") }

func writeLaws(b *strings.Builder, in Input) {
	if len(in.Laws) == 0 {
		return
	}
	b.WriteString("## Candidate value laws\n\nFrom marks labelled `... = <value>`; x is the wire value, y the value read on the surface.\n\n")
	b.WriteString("| Key | Points (x -> y) | Candidate law |\n|---|---|---|\n")
	for _, k := range sortedLawKeys(in.Laws) {
		f := in.Laws[k]
		var pts []string
		for _, p := range f.Points {
			pts = append(pts, fmt.Sprintf("%.6g -> %g", p.X, p.Y))
		}
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", cell(k.String()), cell(strings.Join(pts, ", ")), cell(f.Law.String()))
	}
	b.WriteString("\n")
}

func writeDiffs(b *strings.Builder, in Input) {
	r := in.Result
	if len(r.Snapshots) == 0 {
		return
	}
	b.WriteString("## Snapshots\n\n")
	for _, s := range r.Snapshots {
		fmt.Fprintf(b, "- Snapshot %d %q at %s: %d keys\n", s.ID, s.Label, rel(in, s.At), len(s.Values))
	}
	b.WriteString("\n")
	for _, d := range r.Diffs {
		fmt.Fprintf(b, "### Snapshot %d -> %d: %d changes\n\n", d.From, d.To, len(d.Changes))
		if len(d.Noise) > 0 {
			fmt.Fprintf(b, "%d streaming key(s) left out (they change all the time): ", len(d.Noise))
			for i, c := range d.Noise {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(b, "`%s`", cell(c.Key.String()))
			}
			b.WriteString(".\n\n")
		}
		b.WriteString("| Key | From | To |\n|---|---|---|\n")
		for _, c := range d.Changes {
			from := c.From.Text
			if c.Added {
				from = "(new)"
			}
			fmt.Fprintf(b, "| `%s` | %s | %s |\n", cell(c.Key.String()), short(cell(from)), short(cell(c.To.Text)))
		}
		b.WriteString("\n")
	}
}

func writeHarvests(b *strings.Builder, in Input) {
	if len(in.Result.Harvests) == 0 {
		return
	}
	b.WriteString("## Initial-sync harvest\n\nEvery key that carried a value after an app connected, until no new key arrived " +
		"for the quiet time. Full lists in harvest.csv.\n\n")
	for _, h := range in.Result.Harvests {
		fmt.Fprintf(b, "- Harvest %d: app %s, %s to %s, **%d keys**\n", h.ID, h.App, rel(in, h.Start), rel(in, h.End), len(h.Keys))
	}
	b.WriteString("\n")
}

func writeKeys(b *strings.Builder, in Input) {
	b.WriteString("## Labelled keys\n\nKeys that were a real (not noise) candidate of a mark, or have a law. Every key is in keys.csv.\n\n")
	b.WriteString("| Key | Label | Direction | Observed values | Candidate law | Evidence | Tag |\n|---|---|---|---|---|---|---|\n")
	for _, k := range in.Result.Keys {
		fit, hasLaw := in.Laws[k.Key]
		if len(k.Labels) == 0 && !hasLaw {
			continue
		}
		lawText := ""
		if hasLaw {
			lawText = fit.Law.String()
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s | %s | %s |\n", cell(k.Key.String()), cell(strings.Join(k.Labels, "; ")),
			k.Dirs, short(cell(values(k, maxShownMD))), cell(lawText), evidence(k), tagCapture)
	}
	b.WriteString("\n")
}

func values(k analyze.KeyInfo, n int) string {
	vs := k.Values
	more := k.MoreValues
	if len(vs) > n {
		vs, more = vs[:n], true
	}
	s := strings.Join(vs, ", ")
	if more {
		s += ", ..."
	}
	return s
}

func evidence(k analyze.KeyInfo) string {
	if k.ChangeEv.File != "" {
		return k.ChangeEv.String()
	}
	return k.FirstEv.String()
}

func rel(in Input, t time.Time) string {
	if in.Meta.Started.IsZero() {
		return t.Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("+%.3f s", t.Sub(in.Meta.Started).Seconds())
}

// cell escapes a markdown table cell.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ", "`", "'").Replace(s)
}

func csvBytes(rows [][]string) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return nil, fmt.Errorf("report: csv: %w", err)
	}
	return buf.Bytes(), nil
}
