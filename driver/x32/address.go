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

	addrXinfo   = "/xinfo"    // X32-F10
	addrXremote = "/xremote"  // X32-F20
	addrMeters  = "/meters"   // X32-F50
	meterBank1  = "/meters/1" // X32-F51, X32-F52
)

// param is one console parameter the driver reads and writes.
type param struct {
	ref  model.ParamRef
	addr string
	typ  osc.Type
}

// paramTable maps between model parameters and X32 addresses.
type paramTable struct {
	all    []param // fixed read order: inputs 1..n (name, fader, mute), then main
	byAddr map[string]int
	byPar  map[model.ParamRef]int
}

func newParamTable(inputs int) paramTable {
	t := paramTable{byAddr: map[string]int{}, byPar: map[model.ParamRef]int{}}
	for i := 1; i <= inputs; i++ {
		strip := model.StripRef{Kind: model.StripInput, Index: i}
		t.add(strip, model.FieldName, fmt.Sprintf("/ch/%02d/config/name", i), osc.TypeString) // X32-F31
		t.add(strip, model.FieldFader, fmt.Sprintf("/ch/%02d/mix/fader", i), osc.TypeFloat)   // X32-F32
		t.add(strip, model.FieldMute, fmt.Sprintf("/ch/%02d/mix/on", i), osc.TypeInt)         // X32-F33
	}
	t.add(model.MainRef, model.FieldFader, "/main/st/mix/fader", osc.TypeFloat) // X32-F32, X32-F34
	t.add(model.MainRef, model.FieldMute, "/main/st/mix/on", osc.TypeInt)       // X32-F33, X32-F34
	return t
}

func (t *paramTable) add(strip model.StripRef, f model.Field, addr string, typ osc.Type) {
	p := param{ref: model.ParamRef{Strip: strip, Field: f}, addr: addr, typ: typ}
	t.byAddr[addr] = len(t.all)
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
