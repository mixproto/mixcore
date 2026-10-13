// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/wingproto"
)

// The schema walk asks only for definitions (DA DD, D7 <hash> DD; WG-F31,
// WG-F33) of the nodes mixcore models, level by level, never for the whole
// tree ("more than 25000 elements", wing.md 2.4) and never with a value
// token. Each level lists the nodes whose children it asks for; "#" stands
// for the numbered children of the parent, up to the parent's documented
// count (WG-F02, WG-F03).
var walkLevels = [][]string{
	{"/ch", "/main", "/dca", "/mgrp", "/io", "/cfg", "/$syscfg"},
	{"/ch/#", "/main/1", "/dca/#", "/mgrp/#", "/io/in"},
	{"/ch/#/in", "/io/in/LCL", "/io/in/A", "/io/in/B"},
	{"/ch/#/in/conn", "/io/in/LCL/#", "/io/in/A/#", "/io/in/B/#"},
}

// numbered is the highest numbered child walked under each parent.
var numbered = map[string]int{
	"/ch": maxInputs, "/dca": maxDCAStrips, "/mgrp": maxMuteGroups,
	"/io/in/LCL": maxLocalIns, "/io/in/A": maxAES50Ins, "/io/in/B": maxAES50Ins,
}

// walkNode is one node whose children a level asks for.
type walkNode struct {
	path string
	hash uint32
	root bool
}

// walk reads the definitions of every modelled node, keyed by path.
func (s *session) walk(ctx context.Context) (map[string]wingproto.Def, error) {
	defs := map[string]wingproto.Def{}
	level := []walkNode{{root: true}}
	for depth := 0; ; depth++ {
		got, err := s.walkLevel(ctx, level)
		if err != nil {
			return nil, err
		}
		for i, n := range level {
			for _, d := range got[i] {
				defs[n.path+"/"+d.Name] = d
			}
		}
		if depth >= len(walkLevels) {
			return defs, nil
		}
		level = expandLevel(walkLevels[depth], defs)
		if len(level) == 0 {
			return defs, nil
		}
	}
}

// expandLevel turns patterns into the nodes the schema has.
func expandLevel(patterns []string, defs map[string]wingproto.Def) []walkNode {
	var out []walkNode
	for _, pat := range patterns {
		for _, path := range expand(pat, defs) {
			if d, ok := defs[path]; ok && d.Type == wingproto.TypeNode {
				out = append(out, walkNode{path: path, hash: d.Hash})
			}
		}
	}
	return out
}

// expand resolves "#" segments against the definitions read so far.
func expand(pattern string, defs map[string]wingproto.Def) []string {
	paths := []string{""}
	for _, seg := range strings.Split(strings.TrimPrefix(pattern, "/"), "/") {
		var next []string
		for _, p := range paths {
			if seg != "#" {
				next = append(next, p+"/"+seg)
				continue
			}
			for n := 1; n <= numbered[p]; n++ {
				child := p + "/" + strconv.Itoa(n)
				if _, ok := defs[child]; !ok {
					break
				}
				next = append(next, child)
			}
		}
		paths = next
	}
	return paths
}

// walkLevel sends the definition requests of one level at once and collects
// the answers. A definition belongs to the requested node whose hash is its
// parent (WG-F33); on the root level every definition belongs to the root.
// The level ends when one DE per request arrived (WG-F30). Should a console
// send no DE for a node without children, the level also ends after
// WalkQuiet of silence once there are at least as many DEs as nodes that
// answered. The caller makes sure no data request is unanswered before a
// level starts (a DE after a data reply is unverified, item 3).
func (s *session) walkLevel(ctx context.Context, nodes []walkNode) ([][]wingproto.Def, error) {
	var req []byte
	byParent := map[uint32]int{}
	for i, n := range nodes {
		if n.root {
			req = wingproto.AppendDefRequest(wingproto.AppendRoot(req))
			continue
		}
		req = wingproto.AppendDefsOf(req, n.hash)
		byParent[n.hash] = i
	}
	if err := s.c.writeData(req); err != nil {
		return nil, lost(err)
	}
	out := make([][]wingproto.Def, len(nodes))
	ends, answered := 0, 0
	for ends < len(nodes) {
		m, ok, err := s.next(ctx, s.timing.WalkQuiet)
		if err != nil {
			return nil, fmt.Errorf("schema walk: %w", err)
		}
		switch {
		case !ok && answered > 0 && ends >= answered:
			return out, nil
		case !ok:
		case m.kind == msgDef:
			i, known := byParent[m.def.Parent]
			if !known && len(nodes) == 1 {
				i, known = 0, true
			}
			if !known {
				continue
			}
			if len(out[i]) == 0 {
				answered++
			}
			out[i] = append(out[i], m.def)
		case m.kind == msgEnd:
			ends++
		}
	}
	return out, nil
}

// next waits up to wait for one message. ok is false on timeout.
func (s *session) next(ctx context.Context, wait time.Duration) (inMsg, bool, error) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case m := <-s.c.inbound:
		return m, true, nil
	case err := <-s.c.errs:
		return inMsg{}, false, lost(err)
	case <-t.C:
		return inMsg{}, false, nil
	case <-s.done:
		return inMsg{}, false, driver.ErrClosed
	case <-ctx.Done():
		return inMsg{}, false, fmt.Errorf("%w: %w", ErrNoReply, context.Cause(ctx))
	}
}
