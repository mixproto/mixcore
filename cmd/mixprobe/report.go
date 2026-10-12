// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status of one checked fact.
type Status string

// Statuses.
const (
	Confirmed    Status = "confirmed"    // the console behaved as docs/protocols/x32.md says
	Refuted      Status = "refuted"      // it behaved differently: update the doc first
	Inconclusive Status = "inconclusive" // the check ran but cannot decide
	Skipped      Status = "skipped"      // not run (operator skipped, or a precondition failed)
	Measured     Status = "measured"     // a number for the doc (rate, latency), no expectation
)

// Finding is the result of one check.
type Finding struct {
	ID       string            `json:"id"`
	Facts    []string          `json:"facts"` // docs/protocols/x32.md ids
	Title    string            `json:"title"`
	Status   Status            `json:"status"`
	Detail   string            `json:"detail"`
	Evidence map[string]string `json:"evidence,omitempty"`
	At       time.Time         `json:"at"`
}

// Report is what one session found.
type Report struct {
	Tool       string            `json:"tool"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt"`
	Console    map[string]string `json:"console"`
	Settings   map[string]string `json:"settings"`
	Findings   []Finding         `json:"findings"`
	Restored   []restoreResult   `json:"restored"`
	Aborted    string            `json:"aborted,omitempty"`

	mu sync.Mutex
}

// add records a finding and echoes it to out.
func (r *Report) add(out io.Writer, f Finding) {
	f.At = time.Now()
	r.mu.Lock()
	r.Findings = append(r.Findings, f)
	r.mu.Unlock()
	fmt.Fprintf(out, "  [%s] %s: %s. %s\n", f.Status, f.ID, f.Title, f.Detail)
}

// write stores report.json and report.md in dir, a directory the operator
// chose on the command line.
func (r *Report) write(dir string) error {
	root, err := openReportDir(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("mixprobe: encode report: %w", err)
	}
	if err := root.WriteFile("report.json", b, 0o600); err != nil {
		return fmt.Errorf("mixprobe: write report: %w", err)
	}
	if err := root.WriteFile("report.md", []byte(r.markdown()), 0o600); err != nil {
		return fmt.Errorf("mixprobe: write report: %w", err)
	}
	return nil
}

// openReportDir creates dir if needed and opens it as a root, so the report
// files cannot land outside it.
func openReportDir(dir string) (*os.Root, error) {
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G703: the operator chooses the report directory
		return nil, fmt.Errorf("mixprobe: report dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("mixprobe: report dir: %w", err)
	}
	return root, nil
}

func (r *Report) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# mixprobe report\n\n")
	fmt.Fprintf(&b, "Started %s, finished %s. Times in the tables are local time of day.\n\n",
		r.StartedAt.Format(time.RFC3339), r.FinishedAt.Format(time.RFC3339))
	if r.Aborted != "" {
		fmt.Fprintf(&b, "**Aborted:** %s\n\n", r.Aborted)
	}
	writeMap(&b, "Console", r.Console)
	writeMap(&b, "Settings", r.Settings)
	fmt.Fprintf(&b, "## Findings\n\n| Check | Facts | Status | Detail | At |\n|---|---|---|---|---|\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "| %s: %s | %s | **%s** | %s | %s |\n", f.ID, cell(f.Title), strings.Join(f.Facts, ", "),
			f.Status, cell(f.Detail), stamp(f.At))
	}
	fmt.Fprintf(&b, "\n## Evidence\n\n")
	for _, f := range r.Findings {
		if len(f.Evidence) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", f.ID)
		for _, k := range sortedKeys(f.Evidence) {
			fmt.Fprintf(&b, "- %s: %s\n", k, f.Evidence[k])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Restored parameters\n\n| Address | Snapshot | Read back | OK |\n|---|---|---|---|\n")
	for _, x := range r.Restored {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %t |\n", x.Address, x.Value, x.ReadBack, x.OK)
	}
	return b.String()
}

func writeMap(b *strings.Builder, title string, m map[string]string) {
	fmt.Fprintf(b, "## %s\n\n", title)
	for _, k := range sortedKeys(m) {
		fmt.Fprintf(b, "- %s: %s\n", k, m[k])
	}
	b.WriteString("\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cell(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "/"), "\n", " ") }

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05.000")
}

func ms(d time.Duration) string { return fmt.Sprintf("%d ms", d.Milliseconds()) }
