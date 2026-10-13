// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"strings"
)

// Node types and units of a definition (WG-F33).
const (
	typeNode       = 0
	typeLinFloat   = 1
	typeLogFloat   = 2
	typeFader      = 3
	typeInt        = 4
	typeStringEnum = 5
	typeFloatEnum  = 6
	typeString     = 7

	unitNone = 0
	unitDB   = 1
	unitMS   = 3
	unitHz   = 4

	flagReadOnly = 1 << 9
)

// Hashes the protocol document lists (WG-F32, wing.md 3.6; WG-F24 for the
// derived values of ch 1). Every other hash of the fixture is a placeholder
// (placeholderHash): the console's hash function is not documented, and a
// client must learn hashes from the console, which is what the simulator
// tests.
var documentedHashes = map[string]uint32{
	"/ch":                0x70B10139,
	"/ch/1/fdr":          0x38AE75C2,
	"/ch/1/mute":         0xF50F69F8,
	"/ch/1/name":         0xC6AD548D,
	"/ch/1/$fdr":         0x7E463474,
	"/ch/1/$mute":        0x26855CD3,
	"/$ctl/lib/$active":  0xDF6A06E2,
	"/$syscfg/$firmware": 0x9E69815D,
}

// placeholderHash is FNV-1a of the path, never one of the documented
// hashes (collisions are resolved by the tree builder).
func placeholderHash(path string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return h.Sum32()
}

// enumItem is one item of a string or float enum.
type enumItem struct {
	name string
	val  float32
	long string
}

// node is one node or parameter of the tree.
type node struct {
	name, long string
	path       string
	hash       uint32
	index      int // 1-based position under the parent
	parent     *node
	children   []*node
	byName     map[string]*node

	typ, unit  int
	readOnly   bool
	min, max   float32 // lin/log float
	steps      int32
	imin, imax int32 // int
	items      []enumItem
	maxLen     int

	val value
}

func (n *node) isLeaf() bool { return n.typ != typeNode }

// child returns the child called name.
func (n *node) child(name string) *node { return n.byName[name] }

// appendDef encodes the definition of n (WG-F33, firmware 3.x: with parent).
func (n *node) appendDef(dst []byte) []byte {
	var parent uint32
	if n.parent != nil {
		parent = n.parent.hash
	}
	body := binary.BigEndian.AppendUint32(nil, parent)
	body = binary.BigEndian.AppendUint32(body, n.hash)
	body = binary.BigEndian.AppendUint16(body, uint16(n.index))
	body = append(append(body, byte(len(n.name))), n.name...)
	body = append(append(body, byte(len(n.long))), n.long...)
	flags := uint16(n.unit&0x0F) | uint16(n.typ&0x0F)<<4
	if n.readOnly {
		flags |= flagReadOnly
	}
	body = binary.BigEndian.AppendUint16(body, flags)
	body = n.appendTypeFields(body)
	dst = append(dst, 0xDF)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(body)))
	return append(dst, body...)
}

func (n *node) appendTypeFields(body []byte) []byte {
	switch n.typ {
	case typeLinFloat, typeLogFloat:
		body = binary.BigEndian.AppendUint32(body, math.Float32bits(n.min))
		body = binary.BigEndian.AppendUint32(body, math.Float32bits(n.max))
		body = binary.BigEndian.AppendUint32(body, uint32(n.steps))
	case typeInt:
		body = binary.BigEndian.AppendUint32(body, uint32(n.imin))
		body = binary.BigEndian.AppendUint32(body, uint32(n.imax))
	case typeStringEnum, typeFloatEnum:
		body = binary.BigEndian.AppendUint16(body, uint16(len(n.items)))
		for _, it := range n.items {
			if n.typ == typeStringEnum {
				body = append(append(body, byte(len(it.name))), it.name...)
			} else {
				body = binary.BigEndian.AppendUint32(body, math.Float32bits(it.val))
			}
			body = append(append(body, byte(len(it.long))), it.long...)
		}
	case typeString:
		body = binary.BigEndian.AppendUint16(body, uint16(n.maxLen))
	}
	return body
}

// tree is the whole parameter tree with a hash index.
type tree struct {
	overrides map[string]uint32 // Options.HashOverrides
	root      *node
	byHash    map[uint32]*node
	byPath    map[string]*node
}

func newTree() *tree {
	r := &node{byName: map[string]*node{}}
	return &tree{root: r, byHash: map[uint32]*node{}, byPath: map[string]*node{"": r}}
}

// add creates (or returns) the node at path, creating parent nodes.
func (t *tree) add(path string, leaf node) *node {
	if n, ok := t.byPath[path]; ok {
		return n
	}
	i := strings.LastIndexByte(path, '/')
	parent := t.add(path[:i], node{typ: typeNode})
	leaf.name, leaf.path, leaf.parent = path[i+1:], path, parent
	if leaf.long == "" {
		leaf.long = leaf.name
	}
	leaf.index = len(parent.children) + 1
	leaf.byName = map[string]*node{}
	leaf.hash = t.hashFor(path)
	n := &leaf
	parent.children = append(parent.children, n)
	parent.byName[n.name] = n
	t.byHash[n.hash] = n
	t.byPath[path] = n
	return n
}

// hashFor is the documented hash, else a placeholder unique in the tree.
func (t *tree) hashFor(path string) uint32 {
	if h, ok := t.overrides[path]; ok {
		return h
	}
	if h, ok := documentedHashes[path]; ok {
		return h
	}
	h := placeholderHash(path)
	for t.byHash[h] != nil || isDocumented(h) || h == 0 {
		h++
	}
	return h
}

func isDocumented(h uint32) bool {
	for _, d := range documentedHashes {
		if d == h {
			return true
		}
	}
	return false
}

// leaves returns every leaf under n in tree order.
func leaves(n *node, out []*node) []*node {
	if n.isLeaf() {
		return append(out, n)
	}
	for _, c := range n.children {
		out = leaves(c, out)
	}
	return out
}

// remove deletes the node at path and everything below it.
func (t *tree) remove(path string) {
	n, ok := t.byPath[path]
	if !ok || n.parent == nil {
		return
	}
	for _, l := range append(leaves(n, nil), n) {
		delete(t.byPath, l.path)
		delete(t.byHash, l.hash)
	}
	p := n.parent
	delete(p.byName, n.name)
	for i, c := range p.children {
		if c == n {
			p.children = append(p.children[:i:i], p.children[i+1:]...)
			break
		}
	}
}
