// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// Strip counts (WG-F02, WG-F03) and how mixcore maps them today.
const (
	maxInputs     = 40 // /ch/1..40
	maxDCAStrips  = 16 // /dca/1..16
	maxMuteGroups = 8  // /mgrp/1..8
	mainStrip     = 1  // /main/1 is the model's main (open question 4 default)
	maxLocalIns   = 24 // /io/in/LCL/1..24 (WG-F03)
	maxAES50Ins   = 48 // one AES50 port
	// Headamp indexes of the model: LCL n is n, AES50 A n is 100 + n, B n is
	// 200 + n (contracts/core-api config.schema.json, family wing).
	aesAOffset = 100
	aesBOffset = 200
)

// Bank names of /io/in/<grp> and of a channel's conn/grp (wing.md 3.4).
const (
	bankLCL = "LCL"
	bankA   = "A"
	bankB   = "B"
)

// Leaf and node names (wing.md 3.2, 3.4, 3.5).
const (
	leafFader     = "fdr"
	leafMute      = "mute"
	leafName      = "name"
	leafShownName = "$name"
	leafShownMute = "$mute"
	leafTags      = "tags"
	leafGain      = "g"
	leafPhantom   = "vph"
	nodeIn        = "in"
	nodeConn      = "conn"
	leafSrcGroup  = "grp"
	leafSrcIndex  = "in"
	pathDCAMgrp   = "/cfg/dcamgrp"
	pathTCPLock   = "/$syscfg/tcplock"
	pathFirmware  = "/$syscfg/$firmware"
	pathModel     = "/$syscfg/$cnsmdl"
	pathName      = "/$syscfg/consolename"
)

// errSchema: the console's definitions lack something mixcore needs.
var errSchema = errors.New("wing: console schema lacks a required node")

// conv says how a parameter's wire value maps to the model.
type conv uint8

const (
	convFader       conv = iota + 1 // D5 dB (WG-F60)
	convMute                        // int, 1 = muted / engaged (wing.md 3.2)
	convName                        // string; read $name, write name (WG-F107)
	convTags                        // string, DCA membership (wing.md 3.2), read-only
	convConsoleMute                 // <s>/$mute: the console's effective mute (WG-F24), read-only
	convGain                        // D5 dB on the headamp's law (WG-F62)
	convPhantom                     // int, 1 = on, read-only (wing.md 3.4)
	convSrcGroup                    // enum, a channel's source group (internal)
	convSrcIndex                    // int 1..64, a channel's source number (internal)
	convDCAMgrp                     // int: DCA mute mutes members (wing.md 3.2, internal)
	convTCPLock                     // int: remote lock for TCP (WG-F26, internal)
	convIdentity                    // string, identity (wing.md 3.5, internal)
)

// leafType is the node type each conversion expects (WG-F33): a fader is
// a fader level, a mute an int (review H2). A leaf of another type is not
// the parameter mixcore models: absent, or a schema error when required.
var leafType = map[conv]wingproto.NodeType{
	convFader: wingproto.TypeFader, convMute: wingproto.TypeInt, convName: wingproto.TypeString,
	convTags: wingproto.TypeString, convGain: wingproto.TypeLinFloat, convPhantom: wingproto.TypeInt,
	convSrcGroup: wingproto.TypeStringEnum, convSrcIndex: wingproto.TypeInt, convDCAMgrp: wingproto.TypeInt,
	convTCPLock: wingproto.TypeInt, convIdentity: wingproto.TypeString, convConsoleMute: wingproto.TypeInt,
}

// leaf returns the definition at path when it has the type c expects.
func leaf(defs map[string]wingproto.Def, path string, c conv) (wingproto.Def, bool) {
	d, ok := defs[path]
	return d, ok && d.Type == leafType[c]
}

// param is one console parameter the session reads (and maybe writes).
type param struct {
	ref       model.ParamRef // zero for internal parameters
	path      string         // read path, for logs
	writePath string         // write path, for the write audit; differs only for names
	hash      uint32         // read hash (WG-F32)
	writeHash uint32         // write hash; differs only for names (WG-F107)
	conv      conv
	readOnly  bool
	required  bool // connect fails without its value
	headamp   int  // convGain, convPhantom: model headamp index
	input     int  // convSrcGroup, convSrcIndex, convTags: the input strip
	law       gainLaw
	maxLen    int // convName: longest stored name
	items     []wingproto.EnumItem
}

// headampDef describes one headamp the schema has.
type headampDef struct {
	index, number int
	bank          model.HeadampBank
	law           gainLaw
	writable      bool
	gain, phantom int // parameter indexes; phantom -1 when absent
}

// table is every parameter of one schema. Immutable once built.
type table struct {
	all      []param
	byHash   map[uint32]int
	byRef    map[model.ParamRef]int
	inputs   int
	hasMain  bool
	dcas     int
	groups   int
	headamps []headampDef // LCL, then AES50 A, then B, by number
	identity map[string]int
	srcGroup []int // per input (index input-1): parameter index, -1 when absent
	srcIndex []int
	tags     []int
}

// buildTable makes the table from the definitions the schema walk read.
func buildTable(defs map[string]wingproto.Def) (*table, error) {
	t := &table{byHash: map[uint32]int{}, byRef: map[model.ParamRef]int{}, identity: map[string]int{}}
	if err := t.addInputs(defs); err != nil {
		return nil, err
	}
	if err := t.addMain(defs); err != nil {
		return nil, err
	}
	t.dcas = t.addGroups(defs, "/dca", maxDCAStrips, model.StripDCA)
	t.groups = t.addGroups(defs, "/mgrp", maxMuteGroups, model.StripMuteGroup)
	t.addHeadamps(defs)
	for _, p := range []struct {
		path string
		c    conv
	}{{pathDCAMgrp, convDCAMgrp}, {pathTCPLock, convTCPLock}, {pathFirmware, convIdentity}, {pathModel, convIdentity}, {pathName, convIdentity}} {
		if d, ok := leaf(defs, p.path, p.c); ok {
			i := t.add(param{path: p.path, hash: d.Hash, conv: p.c, readOnly: true})
			if p.c == convIdentity {
				t.identity[p.path] = i
			}
		}
	}
	return t, nil
}

// add appends p and indexes it. A hash already present keeps its first
// parameter (two paths cannot share a hash on one console, WG-F32).
func (t *table) add(p param) int {
	if p.writeHash == 0 {
		p.writeHash = p.hash
	}
	if p.writePath == "" {
		p.writePath = p.path
	}
	if i, dup := t.byHash[p.hash]; dup {
		return i
	}
	i := len(t.all)
	t.all = append(t.all, p)
	t.byHash[p.hash] = i
	if p.ref != (model.ParamRef{}) {
		t.byRef[p.ref] = i
	}
	return i
}

func pref(kind model.StripKind, index int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: kind, Index: index}, Field: f}
}

// addStripCore adds fader, mute and name of one strip root; all required.
func (t *table) addStripCore(defs map[string]wingproto.Def, root string, s model.StripRef) error {
	fdr, okF := leaf(defs, root+"/"+leafFader, convFader)
	mute, okM := leaf(defs, root+"/"+leafMute, convMute)
	name, okN := leaf(defs, root+"/"+leafName, convName)
	if !okF || !okM || !okN {
		return fmt.Errorf("%w: %s fdr, mute or name missing or of another type", errSchema, root)
	}
	shown, okS := leaf(defs, root+"/"+leafShownName, convName)
	if !okS {
		shown = name // without $name, read name itself
	}
	t.add(param{ref: model.ParamRef{Strip: s, Field: model.FieldFader}, path: root + "/" + leafFader, hash: fdr.Hash, conv: convFader, required: true})
	t.add(param{ref: model.ParamRef{Strip: s, Field: model.FieldMute}, path: root + "/" + leafMute, hash: mute.Hash, conv: convMute, required: true})
	t.optional(defs, param{ref: model.ParamRef{Strip: s, Field: model.FieldConsoleMute}, path: root + "/" + leafShownMute,
		conv: convConsoleMute, readOnly: true})
	t.add(param{ref: model.ParamRef{Strip: s, Field: model.FieldName}, path: root + "/" + leafShownName, hash: shown.Hash,
		writePath: root + "/" + leafName, writeHash: name.Hash, conv: convName, required: true, maxLen: int(name.MaxLen)})
	return nil
}

// addInputs adds /ch/1..N, N the first channel the schema lacks (max 40).
func (t *table) addInputs(defs map[string]wingproto.Def) error {
	for n := 1; n <= maxInputs; n++ {
		root := "/ch/" + strconv.Itoa(n)
		if _, ok := defs[root]; !ok {
			break
		}
		if err := t.addStripCore(defs, root, model.StripRef{Kind: model.StripInput, Index: n}); err != nil {
			return err
		}
		t.inputs = n
		t.tags = append(t.tags, t.optional(defs, param{ref: pref(model.StripInput, n, model.FieldDCAs),
			path: root + "/" + leafTags, conv: convTags, readOnly: true, input: n}))
		conn := root + "/" + nodeIn + "/" + nodeConn + "/"
		grp := param{path: conn + leafSrcGroup, conv: convSrcGroup, readOnly: true, input: n}
		if d, ok := defs[grp.path]; ok {
			grp.items = d.Items
		}
		t.srcGroup = append(t.srcGroup, t.optional(defs, grp))
		t.srcIndex = append(t.srcIndex, t.optional(defs, param{path: conn + leafSrcIndex, conv: convSrcIndex, readOnly: true, input: n}))
	}
	if t.inputs == 0 {
		return fmt.Errorf("%w: /ch/1", errSchema)
	}
	return nil
}

// optional adds p when its path is in the schema; -1 when it is not.
func (t *table) optional(defs map[string]wingproto.Def, p param) int {
	d, ok := leaf(defs, p.path, p.conv)
	if !ok {
		return -1
	}
	p.hash = d.Hash
	return t.add(p)
}

func (t *table) addMain(defs map[string]wingproto.Def) error {
	root := "/main/" + strconv.Itoa(mainStrip)
	if _, ok := defs[root]; !ok {
		return nil
	}
	if err := t.addStripCore(defs, root, model.MainRef); err != nil {
		return err
	}
	t.hasMain = true
	return nil
}

// addGroups adds <root>/1..N/mute (1 = muted / engaged) and returns N.
func (t *table) addGroups(defs map[string]wingproto.Def, root string, limit int, kind model.StripKind) int {
	n := 0
	for ; n < limit; n++ {
		path := root + "/" + strconv.Itoa(n+1) + "/" + leafMute
		d, ok := leaf(defs, path, convMute)
		if !ok {
			break
		}
		t.add(param{ref: pref(kind, n+1, model.FieldMute), path: path, hash: d.Hash, conv: convMute, required: true})
	}
	return n
}

// addHeadamps adds the gain and phantom of every LCL, AES50 A and B input
// the schema has (wing.md 3.4). Gain is writable on LCL only, with a law
// read from the console (WG-F62); AES50 gain is read-only until item 15.
func (t *table) addHeadamps(defs map[string]wingproto.Def) {
	for _, b := range []struct {
		name   string
		bank   model.HeadampBank
		offset int
		count  int
	}{{bankLCL, model.BankLocal, 0, maxLocalIns}, {bankA, model.BankAES50A, aesAOffset, maxAES50Ins}, {bankB, model.BankAES50B, aesBOffset, maxAES50Ins}} {
		for n := 1; n <= b.count; n++ {
			root := "/io/in/" + b.name + "/" + strconv.Itoa(n) + "/"
			g, ok := leaf(defs, root+leafGain, convGain)
			if !ok {
				break
			}
			h := headampDef{index: b.offset + n, number: n, bank: b.bank, phantom: -1}
			h.law, h.writable = gainLawOf(g)
			h.writable = h.writable && b.bank == model.BankLocal
			ref := pref(model.StripHeadamp, h.index, model.FieldGain)
			h.gain = t.add(param{ref: ref, path: root + leafGain, hash: g.Hash, conv: convGain, readOnly: !h.writable,
				headamp: h.index, law: h.law})
			h.phantom = t.optional(defs, param{ref: pref(model.StripHeadamp, h.index, model.FieldPhantom),
				path: root + leafPhantom, conv: convPhantom, readOnly: true, headamp: h.index})
			t.headamps = append(t.headamps, h)
		}
	}
}

// headampFor is the model index of the headamp a channel source names, or
// 0 when the source has none the table knows (wing.md 3.4).
func (t *table) headampFor(group string, number int) int {
	offset := map[string]int{bankLCL: 0, bankA: aesAOffset, bankB: aesBOffset}
	base, ok := offset[group]
	if !ok || number < 1 {
		return 0
	}
	for _, h := range t.headamps {
		if h.index == base+number {
			return h.index
		}
	}
	return 0
}

// headamp returns the definition of model headamp index.
func (t *table) headamp(index int) (headampDef, bool) {
	for _, h := range t.headamps {
		if h.index == index {
			return h, true
		}
	}
	return headampDef{}, false
}
