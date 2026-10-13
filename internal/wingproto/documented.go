// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"maps"
	"slices"
)

// documentedHashes are the hashes docs/protocols/wing.md lists (WG-F32:
// the same in the 2020 and 2025 documents and a 3.0.5 schema; section 3.6;
// WG-F24). A console that reports another hash for one of these paths is
// not the documented tree.
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

// DocumentedHash returns the documented hash of path, if the document lists one.
func DocumentedHash(path string) (uint32, bool) {
	h, ok := documentedHashes[path]
	return h, ok
}

// DocumentedPaths lists the paths with a documented hash, sorted.
func DocumentedPaths() []string { return slices.Sorted(maps.Keys(documentedHashes)) }
