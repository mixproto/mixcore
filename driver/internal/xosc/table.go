// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"fmt"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// Addresses both families use (X32-F10, X32-F20, X32-F50; XA-F10, XA-F20, XA-F50).
const (
	addrXinfo   = "/xinfo"
	addrXremote = "/xremote"
	addrMeters  = "/meters"
)

// Layout is the family's address tree for the parameters the driver models.
// The leaf names and polarities are the same in both families (xair.md
// section 3: "Same as X32"); counts and the main root differ.
type Layout struct {
	// Inputs are /ch/01../ch/NN, always two digits (X32-F30, XA section 3.1).
	Inputs int
	// RequiredInputs must answer for a connection; inputs above it are
	// optional strips that are absent for the session when they do not
	// answer (XA-F72). Equal to Inputs when every input is required.
	RequiredInputs int
	// MainRoot is the main LR strip: "/main/st" (X32-F34) or "/lr" (XA section 3.1).
	MainRoot string
	// MainMuteGroups: the main strip has a grp/mute membership (X32-F36);
	// X Air has none (XA section 3.2).
	MainMuteGroups bool
	MuteGroups     int // /config/mute/1..N (X32-F35, XA section 3.1)
	DCAs           int // /dca/1..N/on (X32-F37, XA section 3.1)
}

// conv is how a parameter's wire value maps to the model.
type conv uint8

const (
	convLevel    conv = iota + 1 // float 0..1, fader law (X32-F32, X32-F40, XA-F40)
	convOnSwitch                 // int, 1 = on: model mute = wire 0 (X32-F33, X32-F37)
	convEngaged                  // int, 1 = engaged: model mute = wire 1 (X32-F35)
	convName                     // string (X32-F31)
	convMask                     // int bitmap 0..maxMask (X32-F36, X32-F38)
)

// feature groups parameters that only exist together; if the console does
// not answer one of them, the whole feature is absent (never faked).
type feature uint8

const (
	featureCore feature = iota // names, faders, mutes: required for a connection
	featureMuteGroups
	featureDCAs
)

// param is one console parameter the driver reads and maybe writes.
type param struct {
	ref      model.ParamRef
	addr     string
	typ      osc.Type
	conv     conv
	maxMask  int32
	feature  feature
	readOnly bool
	// strip is the input index of an optional strip (XA-F72), 0 otherwise.
	strip int
}

// optional reports whether the console may leave p unanswered.
func (p param) optional() bool { return p.feature != featureCore || p.strip != 0 }

// paramTable maps between model parameters and console addresses.
type paramTable struct {
	all    []param // fixed read order: inputs, main, mute groups, DCAs
	byAddr map[string]int
	byPar  map[model.ParamRef]int
	layout Layout
}

func newParamTable(l Layout) paramTable {
	t := paramTable{byAddr: map[string]int{}, byPar: map[model.ParamRef]int{}, layout: l}
	mgMask, dcaMask := int32(1)<<l.MuteGroups-1, int32(1)<<l.DCAs-1 // X32-F36, X32-F38; XA: 0..15
	for i := 1; i <= l.Inputs; i++ {
		strip := model.StripRef{Kind: model.StripInput, Index: i}
		ch := fmt.Sprintf("/ch/%02d", i)
		opt := 0
		if i > l.RequiredInputs {
			opt = i
		}
		t.add(param{ref: pref(strip, model.FieldName), addr: ch + "/config/name", typ: osc.TypeString, conv: convName, strip: opt})
		t.add(param{ref: pref(strip, model.FieldFader), addr: ch + "/mix/fader", typ: osc.TypeFloat, conv: convLevel, strip: opt})
		t.add(param{ref: pref(strip, model.FieldMute), addr: ch + "/mix/on", typ: osc.TypeInt, conv: convOnSwitch, strip: opt})
		t.add(membership(strip, model.FieldMuteGroups, ch+"/grp/mute", mgMask, featureMuteGroups, opt))
		t.add(membership(strip, model.FieldDCAs, ch+"/grp/dca", dcaMask, featureDCAs, opt))
	}
	main := model.MainRef
	t.add(param{ref: pref(main, model.FieldFader), addr: l.MainRoot + "/mix/fader", typ: osc.TypeFloat, conv: convLevel})
	t.add(param{ref: pref(main, model.FieldMute), addr: l.MainRoot + "/mix/on", typ: osc.TypeInt, conv: convOnSwitch})
	if l.MainMuteGroups {
		t.add(membership(main, model.FieldMuteGroups, l.MainRoot+"/grp/mute", mgMask, featureMuteGroups, 0))
	}
	for g := 1; g <= l.MuteGroups; g++ {
		t.add(param{ref: pref(model.StripRef{Kind: model.StripMuteGroup, Index: g}, model.FieldMute),
			addr: fmt.Sprintf("/config/mute/%d", g), typ: osc.TypeInt, conv: convEngaged, feature: featureMuteGroups})
	}
	for d := 1; d <= l.DCAs; d++ {
		t.add(param{ref: pref(model.StripRef{Kind: model.StripDCA, Index: d}, model.FieldMute),
			addr: fmt.Sprintf("/dca/%d/on", d), typ: osc.TypeInt, conv: convOnSwitch, feature: featureDCAs})
	}
	return t
}

func pref(strip model.StripRef, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: strip, Field: f}
}

// membership is a read-only group bitmap of a strip.
func membership(strip model.StripRef, f model.Field, addr string, maxMask int32, ft feature, opt int) param {
	return param{ref: pref(strip, f), addr: addr, typ: osc.TypeInt, conv: convMask, maxMask: maxMask, feature: ft, readOnly: true, strip: opt}
}

func (t *paramTable) add(p param) {
	t.byAddr[p.addr] = len(t.all)
	t.byPar[p.ref] = len(t.all)
	t.all = append(t.all, p)
}

// byAddress looks up an address without allocating.
func (t *paramTable) byAddress(addr []byte) (param, bool) {
	i, ok := t.byAddr[string(addr)]
	if !ok {
		return param{}, false
	}
	return t.all[i], true
}

func (t *paramTable) byRef(ref model.ParamRef) (param, bool) {
	i, ok := t.byPar[ref]
	if !ok {
		return param{}, false
	}
	return t.all[i], true
}

// index returns the read-order index of p.
func (t *paramTable) index(ref model.ParamRef) int { return t.byPar[ref] }

// Addresses lists every address the driver reads, in read order (tests and
// the protocol docs' read-pass estimates).
func (d *Driver) Addresses() []string {
	out := make([]string, len(d.params.all))
	for i, p := range d.params.all {
		out[i] = p.addr
	}
	return out
}

// Address returns the console address of a model parameter.
func (d *Driver) Address(ref model.ParamRef) (string, bool) {
	p, ok := d.params.byRef(ref)
	return p.addr, ok
}
