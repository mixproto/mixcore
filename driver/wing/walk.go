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
	"unicode/utf8"

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

// Walk limits (review H2): far above a real tree (about 3000 definitions
// for everything mixcore walks), low enough that a console that floods
// definitions cannot exhaust memory.
const (
	maxDefsPerNode = 4096
	maxDefsPerWalk = 60000
	maxWalkBytes   = 16 << 20
	maxNameLen     = 64
)

// errWalkLimit: the console sent more definitions than any WING has.
var errWalkLimit = fmt.Errorf("%w: the console's definitions exceed mixcore's limits", errSchema)

// walkBudget counts what one walk has received.
type walkBudget struct{ defs, bytes int }

func (b *walkBudget) take(size int) error {
	b.defs++
	b.bytes += size
	if b.defs > maxDefsPerWalk || b.bytes > maxWalkBytes {
		return fmt.Errorf("%w: %d definitions, %d bytes", errWalkLimit, b.defs, b.bytes)
	}
	return nil
}

// validName: one path segment of printable text (WG-F30: node names are
// 1..16 bytes; a little room above that, never a slash).
func validName(n string) bool {
	if n == "" || len(n) > maxNameLen || !utf8.ValidString(n) {
		return false
	}
	for _, r := range n {
		if r == '/' || r < ' ' {
			return false
		}
	}
	return true
}

// walkNode is one node whose children a level asks for.
type walkNode struct {
	path string
	hash uint32
	root bool
}

// walk reads the definitions of every modelled node, keyed by path.
func (s *session) walk(ctx context.Context) (map[string]wingproto.Def, error) {
	s.c.walking.Store(true) // definitions are parsed only during a walk (review H2)
	defer s.c.walking.Store(false)
	defs := map[string]wingproto.Def{}
	level := []walkNode{{root: true}}
	var budget walkBudget
	for depth := 0; ; depth++ {
		got, err := s.walkLevel(ctx, level, &budget)
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

// walkLevel asks for the definitions of one level's nodes at once, then
// asks again, one at a time, for every node that got none (review go M2:
// a console may drop requests beyond its pipeline, item 3). A definition
// belongs to the requested node whose hash is its parent (WG-F33); on the
// root level every definition belongs to the root. A node that still has
// no definitions after its own request has no children.
func (s *session) walkLevel(ctx context.Context, nodes []walkNode, budget *walkBudget) ([][]wingproto.Def, error) {
	byParent := map[uint32]int{}
	all := make([]int, len(nodes))
	for i, n := range nodes {
		all[i] = i
		if !n.root {
			byParent[n.hash] = i
		}
	}
	out := make([][]wingproto.Def, len(nodes))
	if err := s.askDefs(ctx, nodes, all, byParent, out, budget); err != nil {
		return nil, err
	}
	for i := range nodes {
		if len(out[i]) > 0 {
			continue
		}
		if err := s.askDefs(ctx, nodes, []int{i}, byParent, out, budget); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// askDefs sends the definition requests of the nodes in which and collects
// answers until each of them has definitions and as many DE tokens as
// requests arrived (WG-F30), or WalkQuiet passes without a message.
// Late answers to earlier requests are attributed by parent all the same.
func (s *session) askDefs(ctx context.Context, nodes []walkNode, which []int, byParent map[uint32]int,
	out [][]wingproto.Def, budget *walkBudget) error {
	var req []byte
	for _, i := range which {
		if nodes[i].root {
			req = wingproto.AppendDefRequest(wingproto.AppendRoot(req))
		} else {
			req = wingproto.AppendDefsOf(req, nodes[i].hash)
		}
	}
	if err := s.c.writeData(req); err != nil {
		return lost(err)
	}
	for ends := 0; ends < len(which) || !allHave(out, which); {
		m, ok, err := s.next(ctx, s.timing.WalkQuiet)
		if err != nil {
			return fmt.Errorf("schema walk: %w", err)
		}
		switch {
		case !ok:
			return nil // quiet: unanswered nodes are asked again, or have no children
		case m.kind == msgEnd:
			ends++
		case m.kind == msgDef:
			if err := s.takeDef(nodes, byParent, out, budget, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *session) takeDef(nodes []walkNode, byParent map[uint32]int, out [][]wingproto.Def, budget *walkBudget, m inMsg) error {
	if err := budget.take(m.size); err != nil {
		return err
	}
	i, ok := s.attribute(nodes, byParent, m)
	if !ok {
		return nil
	}
	if len(out[i]) >= maxDefsPerNode {
		return fmt.Errorf("%w: more than %d children of %s", errWalkLimit, maxDefsPerNode, nodes[i].path)
	}
	out[i] = append(out[i], m.def)
	return nil
}

func allHave(out [][]wingproto.Def, which []int) bool {
	for _, i := range which {
		if len(out[i]) == 0 {
			return false
		}
	}
	return true
}

// attribute returns the requested node a definition belongs to: its
// parent's hash (WG-F33); only the root level, with its single request,
// takes any parent. Malformed definitions and names that are not one path
// segment are dropped.
func (s *session) attribute(nodes []walkNode, byParent map[uint32]int, m inMsg) (int, bool) {
	if m.bad || !validName(m.def.Name) {
		return 0, false
	}
	if len(nodes) == 1 && nodes[0].root {
		return 0, true
	}
	i, ok := byParent[m.def.Parent]
	return i, ok
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
