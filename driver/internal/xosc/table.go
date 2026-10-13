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
	// Headamps are the headamp groups in index order (xair.md 3.6, x32.md
	// X32-F70..F74); none: no Gain. Addresses are HeadampAddr formatted with
	// the wire number = model index + HeadampWireOffset, then /gain or
	// /phantom.
	Headamps          []HeadampGroup
	HeadampAddr       string
	HeadampWireOffset int
	// HeadampIndexes: inputs 1..N carry /-ha/NN/index (NN = input - 1), the
	// headamp feeding the channel (x32 X32-F74); 0: not modelled (xair).
	HeadampIndexes int

	// Buses (core-api v0.4, buses.go): mix buses and FX send masters in
	// index order (x32.md X32-F80; xair.md 3.1). Inputs send to each of
	// them at <ch>/mix/MM/... with MM the two-digit bus index (X32-F82,
	// xair.md 3.3).
	Buses []BusGroup
	// SendOn: <ch>/mix/MM/on exists (X32-F82); xair has none (3.3).
	SendOn bool
	// SendPan: <ch>/mix/MM/pan exists (X32-F84, xair.md 3.3).
	SendPan bool
	// SendTapLeaf is the tap leaf, "type" (X32-F85) or "tap" (xair.md
	// 3.3); empty: no tap.
	SendTapLeaf string
	// SendPairScoped: pan and tap exist for odd MM only and belong to the
	// bus pair (X32-F84, X32-F85).
	SendPairScoped bool
	// Matrices are read-only outputs /mtx/NN (MatrixRoot, X32-F87); 0: none.
	Matrices   int
	MatrixRoot string
	// Colours: <strip>/config/color is read on inputs, main, buses,
	// matrices and, with DCAMasters, DCAs (X32-F89, xair.md 3.2).
	Colours bool
	// DCAMasters: /dca/N/fader and /dca/N/config/name (X32-F88, xair.md 3.2).
	DCAMasters bool
}

// HeadampLaw is a linear Gain law: dB = MinDB + StepDB k, f = k / Steps,
// k = 0..Steps (X32-F71, X32-F72, XA-F111, XA-F112).
type HeadampLaw struct {
	MinDB, StepDB float64
	Steps         int
}

// MaxDB is the top of the law.
func (l HeadampLaw) MaxDB() float64 { return l.MinDB + float64(l.Steps)*l.StepDB }

// HeadampRole says when the headamps of a group are read.
type HeadampRole uint8

const (
	// HeadampUnit: optional units probed on the first connect of a link;
	// the ones below the first that does not answer exist (XA-F73).
	HeadampUnit HeadampRole = iota + 1
	// HeadampExtra: read only when every unit answered (xair aux 17 exists
	// on the 18-channel models only, XA-F110).
	HeadampExtra
	// HeadampPatched: read only while an input's /-ha index points at it
	// (x32 AES50, lead decision 2026-10-12).
	HeadampPatched
)

// HeadampGroup is a run of headamps with one law, label bank and role.
type HeadampGroup struct {
	Bank         model.HeadampBank
	First, Count int // model indexes First..First+Count-1; console numbers 1..Count
	Law          HeadampLaw
	Writable     bool // false: shown, never written (law or ownership unconfirmed)
	Phantom      bool // /phantom is read (X32-F73, XA-F113); never written
	Role         HeadampRole
}

func (g HeadampGroup) has(index int) bool { return index >= g.First && index < g.First+g.Count }

func (g HeadampGroup) info(index int) model.HeadampInfo {
	return model.HeadampInfo{Index: index, Bank: g.Bank, Number: index - g.First + 1, MinDB: g.Law.MinDB,
		MaxDB: g.Law.MaxDB(), WriteMaxDB: g.Law.MaxDB() - g.Law.StepDB, StepDB: g.Law.StepDB, // the wire maximum is never written (convert.go gainToWire)
		Writable: g.Writable, HasPhantom: g.Phantom}
}

// maxHeadamp is the largest headamp index of the layout (0: none).
func (l Layout) maxHeadamp() int {
	n := 0
	for _, g := range l.Headamps {
		n = max(n, g.First+g.Count-1)
	}
	return n
}

// headampGroup returns the group of a headamp index.
func (l Layout) headampGroup(index int) (HeadampGroup, bool) {
	for _, g := range l.Headamps {
		if g.has(index) {
			return g, true
		}
	}
	return HeadampGroup{}, false
}

// conv is how a parameter's wire value maps to the model.
type conv uint8

const (
	convLevel    conv = iota + 1 // float 0..1, fader law (X32-F32, X32-F40, XA-F40)
	convOnSwitch                 // int, 1 = on: model mute = wire 0 (X32-F33, X32-F37)
	convEngaged                  // int, 1 = engaged: model mute = wire 1 (X32-F35)
	convName                     // string (X32-F31)
	convMask                     // int bitmap 0..maxMask (X32-F36, X32-F38)
	convGain                     // float 0..1, the headamp's linear Gain law (X32-F71, XA-F111)
	convFlag                     // int, 1 = on: model true = wire 1 (phantom, X32-F73)
	convHaIndex                  // int, headamp wire number or -1 (X32-F74)
	convPan                      // float 0..1 = -100..+100, 101 values (X32-F84, xair.md section 4)
	convTap                      // int 0..5 = model.TapInput..TapGroup (X32-F85, xair.md 3.3)
	convColour                   // int 0..15 (X32-F89, xair.md 3.2)
)

// feature groups parameters that only exist together; if the console does
// not answer one of them, the whole feature is absent (never faked).
type feature uint8

const (
	featureCore feature = iota // names, faders, mutes: required for a connection
	featureMuteGroups
	featureDCAs
	featureHeadampIndex // /-ha/NN/index: unverified (x32.md item 19), absent if unanswered
	featurePhantom      // /headamp/NN/phantom: read-only, absent if unanswered
	// v0.4 (buses.go): each absent if the console does not answer it.
	featureColours
	featureDCAMasters
	featureBusLinks
	featureMatrices
	featureSends
	featureSendOn
	featureSendPan
	featureSendTap
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
	// headamp is the model index of a headamp parameter, 0 otherwise;
	// role is its group's role.
	headamp int
	role    HeadampRole
	// gainMin, gainStep and gainSteps describe convGain.
	gainMin, gainStep float64
	gainSteps         int
	// haOffset and haMax describe convHaIndex: model index = wire - haOffset,
	// 1..haMax.
	haOffset, haMax int
	// levelSteps is the grid of convLevel: 1023 (faders, X32-F41) or 160
	// (send levels, X32-F83, XA-F41); 0 means 1023.
	levelSteps float64
	// bus is the model bus a bus-master or send parameter belongs to (0:
	// none); absent when that bus did not answer. pairBus is the even bus
	// of a link parameter, which needs both.
	bus, pairBus int
	// needs is a second feature the parameter exists only with (a DCA
	// colour needs DCA masters); featureCore (never absent) by default.
	needs feature
}

// halfStep is how far apart two wire floats may be and still be the same
// console value: half a step of this parameter's law (X32-F41, X32-F72).
func (p param) halfStep() float32 {
	if steps := p.wireSteps(); steps > 0 {
		return float32(0.5 / steps)
	}
	return 0
}

// wireSteps is the grid of a float parameter: its valid wire values are
// k/wireSteps (0 for an int or string parameter).
func (p param) wireSteps() float64 {
	switch p.conv {
	case convLevel:
		if p.levelSteps > 0 {
			return p.levelSteps
		}
		return faderSteps
	case convGain:
		return float64(p.gainSteps)
	case convPan:
		return panWireSteps
	default:
		return 0
	}
}

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
	t.addHeadamps(l)
	t.addV04(l)
	return t
}

// addHeadamps adds gain (and phantom) of every headamp, then the /-ha
// index of the inputs that carry one.
func (t *paramTable) addHeadamps(l Layout) {
	for _, g := range l.Headamps {
		for h := g.First; h < g.First+g.Count; h++ {
			strip := model.StripRef{Kind: model.StripHeadamp, Index: h}
			root := fmt.Sprintf(l.HeadampAddr, h+l.HeadampWireOffset)
			t.add(param{ref: pref(strip, model.FieldGain), addr: root + "/gain", typ: osc.TypeFloat, conv: convGain,
				headamp: h, role: g.Role, readOnly: !g.Writable, gainMin: g.Law.MinDB, gainStep: g.Law.StepDB, gainSteps: g.Law.Steps})
			if g.Phantom {
				t.add(param{ref: pref(strip, model.FieldPhantom), addr: root + "/phantom", typ: osc.TypeInt, conv: convFlag,
					headamp: h, role: g.Role, readOnly: true, feature: featurePhantom})
			}
		}
	}
	for i := 1; i <= l.HeadampIndexes; i++ { // X32-F74: 0-based, two digits
		t.add(param{ref: pref(model.StripRef{Kind: model.StripInput, Index: i}, model.FieldHeadamp),
			addr: fmt.Sprintf("/-ha/%02d/index", i-1), typ: osc.TypeInt, conv: convHaIndex, readOnly: true,
			feature: featureHeadampIndex, haOffset: l.HeadampWireOffset, haMax: l.maxHeadamp()})
	}
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

// Codec converts the values of one parameter to and from the wire, with the
// driver's own conversions (round-trip tests at range ends, AC-PS-20).
type Codec struct{ p param }

// Codec returns the codec of a model parameter.
func (d *Driver) Codec(ref model.ParamRef) (Codec, bool) {
	p, ok := d.params.byRef(ref)
	return Codec{p: p}, ok
}

// ToWire converts an engineering value to the wire argument.
func (c Codec) ToWire(v model.Value) (osc.Arg, error) { return toWire(c.p, v) }

// FromWire converts a wire argument to an engineering value.
func (c Codec) FromWire(a osc.Arg) (model.Value, error) { return fromWire(c.p, a) }
