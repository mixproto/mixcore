// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package report

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/rcpinfo"
	"github.com/mixproto/mixcore/internal/capture/session"
)

func csvFiles(in Input) (keys, harvest, marks []byte, err error) {
	if keys, err = csvBytes(keyRows(in)); err != nil {
		return nil, nil, nil, err
	}
	if harvest, err = csvBytes(harvestRows(in)); err != nil {
		return nil, nil, nil, err
	}
	marks, err = csvBytes(markRows(in))
	return keys, harvest, marks, err
}

func keyRows(in Input) [][]string {
	rows := [][]string{{"key", "kind", "decoder", "label", "direction", "count", "changes", "observed_values",
		"candidate_law", "evidence_first", "evidence_change", "varying_offsets", "tag"}}
	for _, k := range in.Result.Keys {
		lawText := ""
		if f, ok := in.Laws[k.Key]; ok {
			lawText = f.Law.String()
		}
		change := ""
		if k.ChangeEv.File != "" {
			change = k.ChangeEv.String()
		}
		var offs []string
		for _, o := range k.Varying {
			offs = append(offs, strconv.Itoa(o))
		}
		rows = append(rows, []string{
			k.Key.String(), string(k.Key.Kind), k.Decoder, strings.Join(k.Labels, "; "), k.Dirs,
			strconv.Itoa(k.Count), strconv.Itoa(k.Changes), values(k, len(k.Values)), lawText,
			k.FirstEv.String(), change, strings.Join(offs, " "), tagCapture,
		})
	}
	return rows
}

func harvestRows(in Input) [][]string {
	rows := [][]string{{"harvest", "app", "key", "kind", "value", "evidence"}}
	for _, h := range in.Result.Harvests {
		for _, kv := range h.Keys {
			rows = append(rows, []string{strconv.Itoa(h.ID), h.App.String(), kv.Key.String(), string(kv.Key.Kind),
				kv.Value.Text, kv.Evidence.String()})
		}
	}
	return rows
}

func markRows(in Input) [][]string {
	rows := [][]string{{"mark", "label", "key", "from", "to", "direction", "changes", "noise", "evidence"}}
	for _, m := range in.Result.Marks {
		for _, c := range m.Candidates {
			from := c.From.Text
			if !c.From.Present {
				from = "(new)"
			}
			rows = append(rows, []string{strconv.Itoa(m.ID), m.Label, c.Key.String(), from, c.To.Text, c.Dir.String(),
				strconv.Itoa(c.Changes), strconv.FormatBool(c.Noisy), c.Evidence.String()})
		}
	}
	return rows
}

func sortedLawKeys(m map[decode.Key]Fit) []decode.Key {
	return slices.SortedFunc(maps.Keys(m), func(x, y decode.Key) int {
		return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.ID, y.ID))
	})
}

// Introspection output files.
const (
	FileParams    = "params.csv"
	FileMeters    = "meters.csv"
	FileIntroMD   = "introspect.md"
	maxIntroMDRow = 200
)

// Table is one introspected table with why reading it stopped.
type Table struct {
	Verb    string
	Entries []rcpinfo.Entry
	Stopped string
}

// WriteIntrospection writes params.csv, meters.csv and introspect.md.
func WriteIntrospection(d *session.Dir, meta session.Meta, device map[string]string, tables []Table) error {
	var md strings.Builder
	md.WriteString("# mixcapture introspection: " + meta.Console + "\n\n> **Privacy.** " + session.PrivacyNote + "\n\n")
	md.WriteString("Read-only queries (devinfo, prminfo, mtrinfo; docs/protocols/yamaha-rcp.md). Traffic in `" + meta.Capture + "`.\n\n")
	for _, k := range slices.Sorted(maps.Keys(device)) {
		md.WriteString("- " + k + ": " + cell(device[k]) + "\n")
	}
	for _, t := range tables {
		name := FileParams
		if t.Verb == rcpinfo.VerbMtrInfo {
			name = FileMeters
		}
		b, err := csvBytes(tableRows(t))
		if err != nil {
			return err
		}
		if err := d.WriteFile(name, b); err != nil {
			return err
		}
		writeTableMD(&md, t, name)
	}
	return d.WriteFile(FileIntroMD, []byte(md.String()))
}

func tableRows(t Table) [][]string {
	header := []string{"index", "address"}
	if t.Verb == rcpinfo.VerbPrmInfo {
		header = append(header, rcpinfo.FieldNames()...)
	} else {
		header = append(header, "fields")
	}
	rows := [][]string{append(header, "raw", "tag")}
	for _, e := range t.Entries {
		row := []string{strconv.Itoa(e.Index), e.Address}
		if t.Verb == rcpinfo.VerbPrmInfo {
			for _, f := range rcpinfo.FieldNames() {
				row = append(row, e.Field(f))
			}
		} else {
			row = append(row, strings.Join(e.Fields, " "))
		}
		rows = append(rows, append(row, e.Raw, tagCapture))
	}
	return rows
}

func writeTableMD(md *strings.Builder, t Table, file string) {
	md.WriteString("\n## " + t.Verb + ": " + strconv.Itoa(len(t.Entries)) + " entries (" + t.Stopped + "), all in " + file + "\n\n")
	md.WriteString("| Index | Address | Fields |\n|---|---|---|\n")
	for i, e := range t.Entries {
		if i == maxIntroMDRow {
			md.WriteString("| ... | see " + file + " | |\n")
			break
		}
		md.WriteString("| " + strconv.Itoa(e.Index) + " | `" + cell(e.Address) + "` | " + cell(strings.Join(e.Fields, " ")) + " |\n")
	}
}
