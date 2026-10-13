// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package winginfo

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/mixproto/mixcore/internal/wingproto"
)

// Node is one walked node or parameter.
type Node struct {
	Path  string
	Def   wingproto.Def
	Value string // leaves only, when values were read
	Err   string // a read that failed
}

// Options of a walk.
type Options struct {
	Roots    []string // paths to walk below; empty: the whole tree
	MaxNodes int      // stop after this many definitions
	Values   bool     // also read each leaf's value (DC)
	// SkipValue, when set, leaves the values of matching leaves unread
	// (DefaultSkipValue: show control and actions).
	SkipValue func(Node) bool
	Progress  func(n int)
}

// Walk reads definitions breadth-first below each root (one request at a
// time), then, with Values, the value of every leaf: values come last
// because a data answer may end with a DE (item 3), which must never be
// taken for the end of a definition answer. It returns what it read and why
// it stopped ("done", "max nodes", or an error text).
func Walk(ctx context.Context, c *Client, o Options) ([]Node, string, error) {
	if o.MaxNodes <= 0 {
		o.MaxNodes = DefaultMaxNodes
	}
	out, stopped, err := walkDefs(ctx, c, o)
	if err != nil || !o.Values {
		return out, stopped, err
	}
	for i := range out {
		if out[i].Def.Type == wingproto.TypeNode {
			continue
		}
		if o.SkipValue != nil && o.SkipValue(out[i]) {
			out[i].Err = "not read (side effects unverified)"
			continue
		}
		if out[i].Value, err = c.Value(ctx, out[i].Def.Hash); err != nil {
			out[i].Err = err.Error()
		}
	}
	return out, stopped, nil
}

func walkDefs(ctx context.Context, c *Client, o Options) ([]Node, string, error) {
	top, err := c.Children(ctx, true, 0)
	if err != nil {
		return nil, err.Error(), err
	}
	var out []Node
	queue := childNodes("", top)
	for len(queue) > 0 {
		if len(out) >= o.MaxNodes {
			return out, "max nodes", nil
		}
		n := queue[0]
		queue = queue[1:]
		if !wanted(n.Path, o.Roots) {
			continue
		}
		if n.Def.Type == wingproto.TypeNode {
			kids, err := c.Children(ctx, false, n.Def.Hash)
			if err != nil {
				return append(out, n), err.Error(), err
			}
			if len(queue)+len(kids) > o.MaxNodes { // the queue is bounded too (review sec L6)
				return append(out, n), "max nodes", nil
			}
			queue = append(queue, childNodes(n.Path, kids)...)
		}
		out = append(out, n)
		if o.Progress != nil {
			o.Progress(len(out))
		}
	}
	return out, "done", nil
}

func childNodes(parent string, defs []wingproto.Def) []Node {
	out := make([]Node, 0, len(defs))
	for _, d := range defs {
		out = append(out, Node{Path: parent + "/" + d.Name, Def: d})
	}
	return out
}

// wanted: a path on the way to, or below, one of the roots.
func wanted(path string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, r := range roots {
		r = strings.TrimSuffix(r, "/")
		if path == r || strings.HasPrefix(path, r+"/") || strings.HasPrefix(r, path+"/") {
			return true
		}
	}
	return false
}

// DefaultSkipValue leaves unread the values under /$ctl (show control) and
// of action nodes ($action, $actionidx, /play/$action ...): whether a
// read of them has side effects is unverified on hardware (review item 14).
func DefaultSkipValue(n Node) bool {
	last := n.Path[strings.LastIndexByte(n.Path, '/')+1:]
	return strings.HasPrefix(n.Path, "/$ctl") || strings.Contains(strings.ToLower(last), "action")
}

// csvCell protects a cell from spreadsheet formula injection (review sec
// L6): a cell starting with = + - @ gets a leading apostrophe.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}

// typeNames of WG-F33 for the table.
var typeNames = []string{"node", "linf", "logf", "fader", "int", "senum", "fenum", "string"}

// CSV renders the nodes as a table: path, hash, parent, index, long name,
// type, unit, read-only, range, steps, enum items, maxlen, value.
func CSV(nodes []Node) ([]byte, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"path", "hash", "parent", "index", "long", "type", "unit", "ro", "min", "max", "steps", "items", "maxlen", "value", "error"})
	for _, n := range nodes {
		d := n.Def
		typ := strconv.Itoa(int(d.Type))
		if int(d.Type) < len(typeNames) {
			typ = typeNames[d.Type]
		}
		lo, hi, steps := rangeOf(d)
		row := []string{n.Path, fmt.Sprintf("%08x", d.Hash), fmt.Sprintf("%08x", d.Parent), strconv.Itoa(int(d.Index)),
			d.LongName, typ, strconv.Itoa(int(d.Unit)), strconv.FormatBool(d.ReadOnly), lo, hi, steps, items(d),
			strconv.Itoa(int(d.MaxLen)), n.Value, n.Err}
		for i := range row {
			row[i] = csvCell(row[i])
		}
		_ = w.Write(row)
	}
	w.Flush()
	return []byte(b.String()), w.Error()
}

func rangeOf(d wingproto.Def) (string, string, string) {
	switch d.Type {
	case wingproto.TypeLinFloat, wingproto.TypeLogFloat:
		return fmt.Sprint(d.Min), fmt.Sprint(d.Max), strconv.Itoa(int(d.Steps))
	case wingproto.TypeInt:
		return strconv.Itoa(int(d.IntMin)), strconv.Itoa(int(d.IntMax)), ""
	}
	return "", "", ""
}

func items(d wingproto.Def) string {
	parts := make([]string, 0, len(d.Items))
	for _, it := range d.Items {
		if d.Type == wingproto.TypeFloatEnum {
			parts = append(parts, fmt.Sprintf("%g=%s", it.Value, it.Long))
		} else {
			parts = append(parts, it.Name)
		}
	}
	return strings.Join(parts, "|")
}

// Markdown summarises a walk for introspect.md.
func Markdown(console, privacy, stopped string, nodes []Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# mixcapture introspection (wing): %s\n\n> **Privacy.** %s\n\n", console, privacy)
	b.WriteString("Read-only requests only: DA DD, D7 <hash> DD, D7 <hash> DC (docs/protocols/wing.md WG-F31, WG-F33).\n\n")
	fmt.Fprintf(&b, "- nodes read: %d; stopped: %s\n", len(nodes), stopped)
	byPath := map[string]Node{}
	for _, n := range nodes {
		byPath[n.Path] = n
	}
	for _, p := range []string{"/$syscfg/$firmware", "/$syscfg/$cnsmdl", "/$syscfg/$cnscfg", "/io/in/LCL/1/g", "/ch/1/in/set/$g", "/dca/1/name", "/mgrp/1/name"} {
		if n, ok := byPath[p]; ok {
			lo, hi, steps := rangeOf(n.Def)
			fmt.Fprintf(&b, "- %s: value %s, range %s..%s, steps %s, maxlen %d\n", p, n.Value, lo, hi, steps, n.Def.MaxLen)
		}
	}
	b.WriteString("\nDocumented hashes (WG-F32):\n\n| path | document | console |\n|---|---|---|\n")
	for _, p := range wingproto.DocumentedPaths() {
		got := "not walked"
		if n, ok := byPath[p]; ok {
			got = fmt.Sprintf("%08x", n.Def.Hash)
		}
		doc, _ := wingproto.DocumentedHash(p)
		fmt.Fprintf(&b, "| %s | %08x | %s |\n", p, doc, got)
	}
	return b.String()
}
