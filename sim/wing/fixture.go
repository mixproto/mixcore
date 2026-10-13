// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"strconv"
)

// Fixture counts (WG-F02, WG-F03; wing.md 3.1).
const (
	channels    = 40
	auxes       = 8
	buses       = 16
	mains       = 4
	matrices    = 8
	dcas        = 16
	muteGroups  = 8
	localIns    = 24 // /io/in/LCL/1..24 in the tree on every model (WG-F03)
	aesIns      = 48
	nameLen     = 16 // wing.md 3.2 (DCA and mgrp: 16 in the console's own schema, WG-F35)
	tagsLen     = 80 // wing.md 3.2
	firmLen     = 64 // wing.md 3.5
	sendBuses   = 16
	sendMatrix  = 8
	faderBottom = -144 // WG-F60: -oo
	faderTop    = 10
	colours30   = 12 // colours 1..12 up to 3.0.6, 1..18 from 3.1 (WG-F09)
	colours31   = 18
	srcMax      = 64 // a channel's source number 1..64 (wing.md 3.4)
)

// Source groups of a channel (wing.md 3.4).
var sourceGroups = []string{"OFF", "LCL", "AUX", "A", "B", "C", "SC", "USB", "CRD", "MOD", "PLAY", "AES", "USR", "OSC", "BUS", "MAIN", "MTX"}

func enum(names ...string) []enumItem {
	out := make([]enumItem, len(names))
	for i, n := range names {
		out[i] = enumItem{name: n, long: n}
	}
	return out
}

// Parameter shapes (WG-F33) with the ranges of wing.md 3.2-3.5.
func pFader() node               { return node{typ: typeFader, unit: unitDB, val: floatVal(faderBottom)} }
func pSwitch() node              { return node{typ: typeInt, imin: 0, imax: 1, val: intVal(0)} }
func pInt(lo, hi, v int32) node  { return node{typ: typeInt, imin: lo, imax: hi, val: intVal(v)} }
func pText(n int, v string) node { return node{typ: typeString, maxLen: n, val: textVal(v)} }
func pEnum(v string, items ...string) node {
	return node{typ: typeStringEnum, items: enum(items...), val: textVal(v)}
}
func pLin(lo, hi float32, steps int32, unit int, v float32) node {
	return node{typ: typeLinFloat, unit: unit, min: lo, max: hi, steps: steps, val: floatVal(v)}
}
func pLog(lo, hi float32, steps int32, unit int, v float32) node {
	return node{typ: typeLogFloat, unit: unit, min: lo, max: hi, steps: steps, val: floatVal(v)}
}
func ro(n node) node { n.readOnly = true; return n }

// buildFixture makes the tree of wing.md section 12 ("Tree fixture").
func buildFixture(o Options) *tree {
	t := newTree()
	colours := int32(colours30)
	if o.is31() {
		colours = colours31
	}
	for n := 1; n <= channels; n++ {
		addInputStrip(t, "/ch/"+strconv.Itoa(n), colours, n)
	}
	for n := 1; n <= auxes; n++ {
		addAuxStrip(t, "/aux/"+strconv.Itoa(n), colours)
	}
	for n := 1; n <= buses; n++ {
		addMixStrip(t, "/bus/"+strconv.Itoa(n), colours)
	}
	for n := 1; n <= mains; n++ {
		addMixStrip(t, "/main/"+strconv.Itoa(n), colours)
	}
	for n := 1; n <= matrices; n++ {
		addMixStrip(t, "/mtx/"+strconv.Itoa(n), colours)
	}
	for n := 1; n <= dcas; n++ {
		root := "/dca/" + strconv.Itoa(n)
		t.add(root+"/name", pText(nameLen, ""))
		t.add(root+"/col", pInt(1, colours, 1))
		t.add(root+"/fdr", pFader())
		t.add(root+"/mute", pSwitch())
		t.add(root+"/led", pSwitch())
	}
	for n := 1; n <= muteGroups; n++ {
		root := "/mgrp/" + strconv.Itoa(n)
		t.add(root+"/name", pText(nameLen, ""))
		t.add(root+"/mute", pSwitch())
	}
	addSources(t, o)
	addConsoleWide(t, o)
	return t
}

// addStripCommon adds the fields every strip with a fader has (wing.md 3.2).
func addStripCommon(t *tree, root string, colours int32) {
	t.add(root+"/name", pText(nameLen, ""))
	t.add(root+"/$name", ro(pText(nameLen, "")))
	t.add(root+"/col", pInt(1, colours, 1))
	t.add(root+"/$col", ro(pInt(1, colours, 1)))
	t.add(root+"/icon", pInt(0, 999, 0))
	t.add(root+"/fdr", pFader())
	t.add(root+"/$fdr", ro(pFader()))
	t.add(root+"/mute", pSwitch())
	t.add(root+"/$mute", ro(pInt(0, 2, 0)))
	t.add(root+"/$muteovr", pSwitch())
	t.add(root+"/pan", pLin(-100, 100, 201, unitNone, 0))
	t.add(root+"/wid", pLin(-150, 150, 61, 2, 100))
	t.add(root+"/$solo", pSwitch())
	t.add(root+"/tags", pText(tagsLen, ""))
	t.add(root+"/mon", pEnum("A", "A", "B", "A+B"))
	t.add(root+"/led", pSwitch())
}

// addInputStrip adds a channel: common fields, source, filters, EQ and a
// dynamics slot with two models (WG-F34), and sends (wing.md 3.3).
func addInputStrip(t *tree, root string, colours int32, n int) {
	addStripCommon(t, root, colours)
	t.add(root+"/in/set/trim", pLin(-18, 18, 361, unitDB, 0))
	t.add(root+"/in/set/inv", pSwitch())
	t.add(root+"/in/conn/grp", pEnum("LCL", sourceGroups...))
	t.add(root+"/in/conn/in", pInt(1, srcMax, int32(n)))
	t.add(root+"/flt/lc", pSwitch())
	t.add(root+"/flt/lcf", pLog(20, 2000, 641, unitHz, 100.2))
	t.add(root+"/eq/on", pSwitch())
	t.add(root+"/eq/1g", pLin(-15, 15, 301, unitDB, 0))
	t.add(root+"/eq/1f", pLog(20, 20000, 961, unitHz, 200))
	t.add(root+"/eq/1q", pLog(0.442, 10, 181, unitNone, 1))
	t.add(root+"/dyn/on", pSwitch())
	t.add(root+"/dyn/mdl", pEnum(dynModelComp, dynModelComp, dynModelExp))
	t.add(root+"/dyn/thr", pLin(-60, 0, 121, unitDB, -20))
	t.add(root+"/ptap", pEnum("POST", "IN", "FILT", "3", "4", "5", "PFL", "AFL", "POST"))
	for b := 1; b <= sendBuses; b++ {
		addSend(t, root+"/send/"+strconv.Itoa(b), b)
	}
	for m := 1; m <= sendMatrix; m++ {
		addSend(t, root+"/send/MX"+strconv.Itoa(m), 0)
	}
	for m := 1; m <= mains; m++ {
		t.add(root+"/main/"+strconv.Itoa(m)+"/on", pInt(0, 1, boolInt(m == 1)))
		t.add(root+"/main/"+strconv.Itoa(m)+"/lvl", pFader())
	}
}

// Dynamics models of the fixture's slot (WG-F34): the slot keeps its hash,
// its name and law follow the loaded model.
const (
	dynModelComp = "COMP"
	dynModelExp  = "EXP"
)

// addSend adds one send with the defaults of the vendor dump (wing.md 3.3):
// buses 1-8 PRE, 9-10 GRP, 11-16 POST; matrices PRE.
func addSend(t *tree, root string, bus int) {
	mode, plink := "PRE", int32(0)
	switch {
	case bus >= 9 && bus <= 10:
		mode, plink = "GRP", 1
	case bus >= 11:
		mode, plink = "POST", 1
	}
	t.add(root+"/on", pSwitch())
	t.add(root+"/lvl", pFader())
	t.add(root+"/pon", pSwitch())
	t.add(root+"/mode", pEnum(mode, "PRE", "POST", "GRP"))
	t.add(root+"/plink", pInt(0, 1, plink))
	t.add(root+"/pan", pLin(-100, 100, 201, unitNone, 0))
}

func addAuxStrip(t *tree, root string, colours int32) {
	addStripCommon(t, root, colours)
	t.add(root+"/in/conn/grp", pEnum("AUX", sourceGroups...))
	t.add(root+"/in/conn/in", pInt(1, srcMax, 1))
}

func addMixStrip(t *tree, root string, colours int32) {
	addStripCommon(t, root, colours)
	t.add(root+"/busmono", pSwitch())
}

// addSources adds /io/in/LCL|A|B|C (wing.md 3.4) with each group's gain law.
func addSources(t *tree, o Options) {
	lcl, aes := lclLaw(o.LocalGainStep), linLaw{-3, 45.5, 98}
	for _, g := range []struct {
		name string
		n    int
		law  linLaw
	}{{"LCL", localIns, lcl}, {"A", aesIns, aes}, {"B", aesIns, aes}, {"C", aesIns, aes}} {
		for n := 1; n <= g.n; n++ {
			root := "/io/in/" + g.name + "/" + strconv.Itoa(n)
			t.add(root+"/mode", pEnum("M", "M", "ST", "M/S"))
			t.add(root+"/g", pLin(g.law.lo, g.law.hi, g.law.steps, unitDB, 0))
			t.add(root+"/vph", pSwitch())
			t.add(root+"/mute", pSwitch())
			t.add(root+"/pol", pSwitch())
			t.add(root+"/name", pText(nameLen, ""))
			t.add(root+"/tags", pText(tagsLen, ""))
			t.add(root+"/$ha", ro(pInt(0, 5, 1)))
		}
	}
}

// linLaw is a linear law: min, max and number of steps (WG-F63).
type linLaw struct {
	lo, hi float32
	steps  int32
}

// lclLaw is the local gain law (WG-F62): the console's own definition says
// -2.5..+45 dB in 20 steps of 2.5 dB (default); the document says -3..+45.5
// in 98 steps of 0.5 dB (option, hardware item 14).
func lclLaw(step float64) linLaw {
	if step == LocalGainStepFine {
		return linLaw{-3, 45.5, 98}
	}
	return linLaw{-2.5, 45, 20}
}

// addConsoleWide adds identity, status, show control and configuration
// (wing.md 3.5, section 7).
func addConsoleWide(t *tree, o Options) {
	t.add("/$syscfg/consolename", pText(nameLen, o.Name))
	t.add("/$syscfg/$firmware", ro(pText(firmLen, o.Firmware)))
	t.add("/$syscfg/$serial", ro(pText(firmLen, o.Serial)))
	t.add("/$syscfg/$cnsmdl", ro(pText(firmLen, o.Model)))
	t.add("/$syscfg/$cnscfg", ro(pText(firmLen, o.Model)))
	t.add("/$syscfg/tcplock", pSwitch())
	t.add("/$syscfg/ipmode", pEnum("DHCP", "DHCP", "STATIC"))
	t.add("/$stat/time", ro(pText(firmLen, "12:00:00")))
	t.add("/$stat/date", ro(pText(firmLen, "2026-10-13")))
	t.add("/$stat/solo", ro(pSwitch()))
	t.add("/$ctl/lib/$scenes", ro(pText(firmLen, "scene_1")))
	t.add("/$ctl/lib/$actidx", ro(pInt(0, 16384, 0)))
	t.add("/$ctl/lib/$active", ro(pText(firmLen, "")))
	t.add("/$ctl/lib/$actshow", ro(pText(firmLen, "I:SHOW1")))
	t.add("/$ctl/lib/$action", pEnum("IDLE", "IDLE", "GOPREV", "GONEXT", "GO", "PREV", "NEXT", "GOTAG"))
	t.add("/$ctl/lib/$actionidx", pInt(0, 16384, 0))
	t.add("/cfg/dcamgrp", pInt(0, 1, 1))
	t.add("/cfg/mainlink", pEnum("OFF", "OFF", "2", "2-3", "2-4"))
	t.add("/cfg/solo/mode", pEnum("LIVE", "LIVE", "STUDIO", "SIP"))
}

func boolInt(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
