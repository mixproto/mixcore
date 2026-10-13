// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"fmt"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

// Buses, sends, matrices, DCA masters and colours (core-api v0.4), from
// docs/protocols/x32.md X32-F80..F89 and xair.md sections 3.1-3.3. Every
// parameter here comes after the v0.3 table, so the v0.3 read order (and
// its wire trace prefix) is unchanged.

const (
	// sendLevelSteps: send levels have 161 known values, k/160 (X32-F83, XA-F41).
	sendLevelSteps = 160.0
	// panWireSteps: pan has 101 known values, k/100, step 2 (X32-F84, xair.md section 4).
	panWireSteps = 100.0
	// colourMax: config/color is 0..15 (X32-F89, xair.md 3.2).
	colourMax = int32(model.ColourCount) - 1
	// linkAddr is the bus pair link (X32-F81, xair.md 3.2): /config/buslink/1-2.
	linkAddr = "/config/buslink/%d-%d"
)

// BusGroup is a run of buses with one address root (X32-F80; xair.md 3.1).
type BusGroup struct {
	// First, Count: model bus indexes First..First+Count-1, which are also
	// the two-digit send slots MM of <ch>/mix/MM (X32-F82, xair.md 3.3).
	First, Count int
	// Root formats the bus root with its number within the group, from 1:
	// "/bus/%02d" (x32), "/bus/%d" and "/fxsend/%d" (xair).
	Root string
	Kind model.BusKind
	// Linkable: odd/even pairs of the group can be linked (X32-F81, xair.md 3.2).
	Linkable bool
	// SendPan: inputs have a send pan into these buses (X32-F84; xair.md
	// 3.3: buses 1..6 only, no send pan to the FX sends 07..10).
	SendPan bool
}

func (g BusGroup) has(index int) bool { return index >= g.First && index < g.First+g.Count }

func (g BusGroup) root(index int) string { return fmt.Sprintf(g.Root, index-g.First+1) }

// busGroup returns the group of a bus index.
func (l Layout) busGroup(index int) (BusGroup, bool) {
	for _, g := range l.Buses {
		if g.has(index) {
			return g, true
		}
	}
	return BusGroup{}, false
}

// maxBus is the largest bus index of the layout (0: none).
func (l Layout) maxBus() int {
	n := 0
	for _, g := range l.Buses {
		n = max(n, g.First+g.Count-1)
	}
	return n
}

// addV04 adds every v0.4 parameter: colours, DCA masters, bus masters, bus
// links, matrices, then the sends of each input.
func (t *paramTable) addV04(l Layout) {
	t.addColours(l)
	t.addDCAMasters(l)
	for _, g := range l.Buses {
		for b := g.First; b < g.First+g.Count; b++ {
			t.addBusMaster(l, g, b)
		}
	}
	t.addLinks(l)
	t.addMatrices(l)
	for i := 1; i <= l.Inputs; i++ {
		t.addSends(l, i)
	}
}

// optStrip is the strip field of input i's parameters (0: a required input).
func optStrip(l Layout, i int) int {
	if i > l.RequiredInputs {
		return i
	}
	return 0
}

func colourParam(strip model.StripRef, root string) param {
	return param{ref: pref(strip, model.FieldColour), addr: root + "/config/color", typ: osc.TypeInt, conv: convColour,
		feature: featureColours, readOnly: true}
}

// addColours adds the colour of every input and of main (X32-F89, xair.md 3.2).
func (t *paramTable) addColours(l Layout) {
	if !l.Colours {
		return
	}
	for i := 1; i <= l.Inputs; i++ {
		p := colourParam(model.StripRef{Kind: model.StripInput, Index: i}, fmt.Sprintf("/ch/%02d", i))
		p.strip = optStrip(l, i)
		t.add(p)
	}
	t.add(colourParam(model.MainRef, l.MainRoot))
}

// addDCAMasters adds fader, name and colour of every DCA (X32-F88, xair.md 3.2).
func (t *paramTable) addDCAMasters(l Layout) {
	if !l.DCAMasters {
		return
	}
	for d := 1; d <= l.DCAs; d++ {
		strip, root := model.StripRef{Kind: model.StripDCA, Index: d}, fmt.Sprintf("/dca/%d", d)
		t.add(param{ref: pref(strip, model.FieldFader), addr: root + "/fader", typ: osc.TypeFloat, conv: convLevel, feature: featureDCAMasters})
		t.add(param{ref: pref(strip, model.FieldName), addr: root + "/config/name", typ: osc.TypeString, conv: convName, feature: featureDCAMasters})
		if l.Colours {
			p := colourParam(strip, root)
			p.needs = featureDCAMasters
			t.add(p)
		}
	}
}

// addBusMaster adds name, colour, fader and mute of bus b (X32-F80, xair.md 3.1, 3.2).
func (t *paramTable) addBusMaster(l Layout, g BusGroup, b int) {
	strip, root := model.StripRef{Kind: model.StripBus, Index: b}, g.root(b)
	t.add(param{ref: pref(strip, model.FieldName), addr: root + "/config/name", typ: osc.TypeString, conv: convName, bus: b})
	if l.Colours {
		p := colourParam(strip, root)
		p.bus = b
		t.add(p)
	}
	t.add(param{ref: pref(strip, model.FieldFader), addr: root + "/mix/fader", typ: osc.TypeFloat, conv: convLevel, bus: b})
	t.add(param{ref: pref(strip, model.FieldMute), addr: root + "/mix/on", typ: osc.TypeInt, conv: convOnSwitch, bus: b})
}

// addLinks adds /config/buslink/A-B of every linkable pair, as the read-only
// field linked of the odd bus (X32-F81, xair.md 3.2).
func (t *paramTable) addLinks(l Layout) {
	for _, g := range l.Buses {
		if !g.Linkable {
			continue
		}
		for b := g.First; b+1 < g.First+g.Count; b += 2 {
			t.add(param{ref: pref(model.StripRef{Kind: model.StripBus, Index: b}, model.FieldLinked),
				addr: fmt.Sprintf(linkAddr, b, b+1), typ: osc.TypeInt, conv: convFlag, readOnly: true,
				feature: featureBusLinks, bus: b, pairBus: b + 1})
		}
	}
}

// addMatrices adds the read-only matrices (X32-F87).
func (t *paramTable) addMatrices(l Layout) {
	for m := 1; m <= l.Matrices; m++ {
		strip, root := model.StripRef{Kind: model.StripMatrix, Index: m}, fmt.Sprintf(l.MatrixRoot, m)
		t.add(param{ref: pref(strip, model.FieldName), addr: root + "/config/name", typ: osc.TypeString, conv: convName, feature: featureMatrices, readOnly: true})
		if l.Colours {
			p := colourParam(strip, root)
			p.needs = featureMatrices
			t.add(p)
		}
		t.add(param{ref: pref(strip, model.FieldFader), addr: root + "/mix/fader", typ: osc.TypeFloat, conv: convLevel, feature: featureMatrices, readOnly: true})
		t.add(param{ref: pref(strip, model.FieldMute), addr: root + "/mix/on", typ: osc.TypeInt, conv: convOnSwitch, feature: featureMatrices, readOnly: true})
	}
}

// addSends adds input i's send to every bus: level, on, pan and tap where
// the layout has them (X32-F82..F85, xair.md 3.3).
func (t *paramTable) addSends(l Layout, i int) {
	in, opt := model.StripRef{Kind: model.StripInput, Index: i}, optStrip(l, i)
	for _, g := range l.Buses {
		for b := g.First; b < g.First+g.Count; b++ {
			root := fmt.Sprintf("/ch/%02d/mix/%02d", i, b) // always two digits (X32-F82, xair.md 3.3)
			send := func(f model.Field, leaf string, typ osc.Type, c conv, ft feature) param {
				return param{ref: model.ParamRef{Strip: in, Field: f, Bus: b}, addr: root + "/" + leaf, typ: typ, conv: c,
					feature: ft, strip: opt, bus: b}
			}
			lvl := send(model.FieldSendLevel, "level", osc.TypeFloat, convLevel, featureSends)
			lvl.levelSteps = sendLevelSteps
			t.add(lvl)
			if l.SendOn {
				t.add(send(model.FieldSendOn, "on", osc.TypeInt, convFlag, featureSendOn))
			}
			pairOnly := l.SendPairScoped && g.Linkable && b%2 == 0 // X32-F84, X32-F85: odd sends only
			if l.SendPan && g.SendPan && !pairOnly {
				t.add(send(model.FieldSendPan, "pan", osc.TypeFloat, convPan, featureSendPan))
			}
			if l.SendTapLeaf != "" && !pairOnly {
				tap := send(model.FieldSendTap, l.SendTapLeaf, osc.TypeInt, convTap, featureSendTap)
				tap.readOnly = true // v0.4: the tap is read-only (contracts/core-api README)
				t.add(tap)
			}
		}
	}
}

// isBusProbe: the fader of a bus is read once per link as its probe; a bus
// whose fader does not answer is absent with every send to it (XA-F72).
func isBusProbe(p param) bool {
	return p.ref.Strip.Kind == model.StripBus && p.ref.Field == model.FieldFader
}

// markProbedBuses marks every bus whose probe answered.
func (s *session) markProbedBuses(rs *readState) {
	for i, p := range s.params.all {
		if isBusProbe(p) && rs.have[i] && !rs.gaveUp[i] {
			s.buses.set(p.bus, true)
		} else if isBusProbe(p) {
			s.log.Warn(s.name+": bus did not answer; absent with its sends until the engine is restarted", "addr", p.addr)
		}
	}
}

// presentBuses lists the buses that answered, in index order.
func (s *session) presentBuses() []int {
	var out []int
	for b := 1; b <= s.params.layout.maxBus(); b++ {
		if s.buses.has(b) {
			out = append(out, b)
		}
	}
	return out
}

// busInfos describes the buses that answered.
func (s *session) busInfos() []model.BusInfo {
	l := s.params.layout
	var out []model.BusInfo
	for _, b := range s.presentBuses() {
		g, _ := l.busGroup(b)
		out = append(out, model.BusInfo{Index: b, Kind: g.Kind, Linkable: g.Linkable, SendPan: l.SendPan && g.SendPan})
	}
	return out
}

// shape is what the state carries, from the layout and the features the
// console answered (never faked).
func (s *session) shape() model.Shape {
	l, has := s.params.layout, func(f feature) bool { return !s.absent[f] }
	buses := len(s.presentBuses()) > 0
	linkable := false
	for _, g := range l.Buses {
		linkable = linkable || g.Linkable
	}
	return model.Shape{
		Colours:    l.Colours && has(featureColours),
		DCAMasters: l.DCAMasters && l.DCAs > 0 && has(featureDCAMasters) && has(featureDCAs),
		BusLinks:   linkable && has(featureBusLinks),
		Sends: model.SendShape{
			Level:      buses && has(featureSends),
			On:         buses && l.SendOn && has(featureSendOn),
			Pan:        buses && l.SendPan && has(featureSendPan),
			Tap:        buses && l.SendTapLeaf != "" && has(featureSendTap),
			PairScoped: l.SendPairScoped,
		},
	}
}
