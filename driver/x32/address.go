// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"fmt"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// Wire facts from docs/protocols/x32.md.
const (
	// DefaultPort is the console's UDP control port (X32-F01).
	DefaultPort = 10023
	// inputChannels: /ch/01../ch/32 (X32-F30).
	inputChannels = 32
	// maxPushClients: /xremote pushes go to at most four clients (X32-F21).
	maxPushClients = 4
	// muteGroupCount: /config/mute/1..6 (X32-F35).
	muteGroupCount = 6
	// dcaCount: /dca/1..8 (X32-F37).
	dcaCount = 8
	// Membership bitmaps: 6 bits for mute groups (X32-F36), 8 for DCAs (X32-F38).
	muteGroupMask = 1<<muteGroupCount - 1
	dcaMask       = 1<<dcaCount - 1

	addrXinfo   = "/xinfo"    // X32-F10
	addrXremote = "/xremote"  // X32-F20
	addrMeters  = "/meters"   // X32-F50
	meterBank1  = "/meters/1" // X32-F51, X32-F52
	meterBank2  = "/meters/2" // X32-F55
)

// conv is how a parameter's wire value maps to the model.
type conv uint8

const (
	convLevel    conv = iota + 1 // float 0..1, fader law (X32-F32, X32-F40)
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
}

// paramTable maps between model parameters and X32 addresses.
type paramTable struct {
	all    []param // fixed read order: inputs, main, mute groups, DCAs
	byAddr map[string]int
	byPar  map[model.ParamRef]int
}

func newParamTable(inputs int) paramTable {
	t := paramTable{byAddr: map[string]int{}, byPar: map[model.ParamRef]int{}}
	for i := 1; i <= inputs; i++ {
		strip := model.StripRef{Kind: model.StripInput, Index: i}
		ch := fmt.Sprintf("/ch/%02d", i) // X32-F30
		t.add(param{ref: pref(strip, model.FieldName), addr: ch + "/config/name", typ: osc.TypeString, conv: convName})
		t.add(param{ref: pref(strip, model.FieldFader), addr: ch + "/mix/fader", typ: osc.TypeFloat, conv: convLevel})
		t.add(param{ref: pref(strip, model.FieldMute), addr: ch + "/mix/on", typ: osc.TypeInt, conv: convOnSwitch})
		t.add(membership(strip, model.FieldMuteGroups, ch+"/grp/mute", muteGroupMask, featureMuteGroups))
		t.add(membership(strip, model.FieldDCAs, ch+"/grp/dca", dcaMask, featureDCAs))
	}
	main := model.MainRef // X32-F34
	t.add(param{ref: pref(main, model.FieldFader), addr: "/main/st/mix/fader", typ: osc.TypeFloat, conv: convLevel})
	t.add(param{ref: pref(main, model.FieldMute), addr: "/main/st/mix/on", typ: osc.TypeInt, conv: convOnSwitch})
	t.add(membership(main, model.FieldMuteGroups, "/main/st/grp/mute", muteGroupMask, featureMuteGroups))
	for g := 1; g <= muteGroupCount; g++ {
		t.add(param{ref: pref(model.StripRef{Kind: model.StripMuteGroup, Index: g}, model.FieldMute),
			addr: fmt.Sprintf("/config/mute/%d", g), typ: osc.TypeInt, conv: convEngaged, feature: featureMuteGroups})
	}
	for d := 1; d <= dcaCount; d++ {
		t.add(param{ref: pref(model.StripRef{Kind: model.StripDCA, Index: d}, model.FieldMute),
			addr: fmt.Sprintf("/dca/%d/on", d), typ: osc.TypeInt, conv: convOnSwitch, feature: featureDCAs})
	}
	return t
}

func pref(strip model.StripRef, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: strip, Field: f}
}

// membership is a read-only group bitmap of a strip.
func membership(strip model.StripRef, f model.Field, addr string, maxMask int32, ft feature) param {
	return param{ref: pref(strip, f), addr: addr, typ: osc.TypeInt, conv: convMask, maxMask: maxMask, feature: ft, readOnly: true}
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
