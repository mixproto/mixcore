// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"fmt"
	"math"
	"strings"
)

// write applies a value token from a client to the selected leaf and pushes
// the change (wing.md section 12 "Push rules"). Every received value token
// is logged as a write, applied or not.
func (s *Sim) write(cl *client, n *node, tok token) {
	if n == nil || !n.isLeaf() {
		s.logEntry(cl, ClassWrite, pathOf(n), tokenText(tok), false)
		return
	}
	v, ok := convert(n, tok, s.opts)
	if ok && s.locked() {
		s.logEntry(cl, ClassWrite, n.path, tokenText(tok), false)
		if s.opts.TCPLockAnswersOld {
			cl.reply = appendPush(cl.reply, n.hash, n.val)
		}
		return
	}
	s.logEntry(cl, ClassWrite, n.path, tokenText(tok), ok && !n.readOnly)
	if !ok || n.readOnly {
		return
	}
	changed, derived := s.set(n, v)
	if len(changed) == 0 {
		return
	}
	if s.opts.EchoToWriter {
		for _, c := range changed {
			cl.reply = appendPush(cl.reply, c.hash, c.val)
		}
	}
	if !s.opts.NoPushOtherClients {
		s.pushAll(changed, cl)
	}
	s.pushAll(derived, nil) // $fdr, $mute, $name follow every change (WG-F24)
}

// locked: /$syscfg/tcplock = 1 prevents modifications from TCP (WG-F26).
func (s *Sim) locked() bool {
	n := s.tree.byPath["/$syscfg/tcplock"]
	return n != nil && n.val.i == 1
}

// set stores v and updates derived values. It returns the changed node
// (none when the value was already v) and the derived nodes that changed.
func (s *Sim) set(n *node, v value) (changed, derived []*node) {
	if n.val == v {
		return nil, nil
	}
	n.val = v
	changed = []*node{n}
	parent := n.parent
	switch n.name {
	case "fdr":
		derived = s.setDerived(parent.child("$fdr"), v)
	case "mute", "tags", "dcamgrp":
		derived = s.recomputeMutes() // own, group and DCA mutes all feed $mute (wing.md 3.2)
	case "name":
		derived = s.setDerived(parent.child("$name"), textVal(s.shownName(parent)))
	case "col":
		derived = s.setDerived(parent.child("$col"), v)
	case "mdl":
		derived = s.swapModel(parent, v.s)
	case "$action":
		derived = s.onAction(v.s)
	}
	return changed, derived
}

func (s *Sim) setDerived(n *node, v value) []*node {
	if n == nil || n.val == v {
		return nil
	}
	n.val = v
	return []*node{n}
}

// shownName is $name: the strip's own name, else its source's name
// ("reflects linked source or current strip value", wing.md 3.2).
func (s *Sim) shownName(strip *node) string {
	if own := strip.child("name"); own != nil && own.val.s != "" {
		return own.val.s
	}
	grp, in := s.tree.byPath[strip.path+"/in/conn/grp"], s.tree.byPath[strip.path+"/in/conn/in"]
	if grp == nil || in == nil {
		return ""
	}
	if src := s.tree.byPath[fmt.Sprintf("/io/in/%s/%d/name", grp.val.s, in.val.i)]; src != nil {
		return src.val.s
	}
	return ""
}

// swapModel follows a dynamics model change (WG-F34): the slot keeps its
// hash; its name and law become those of the new model.
func (s *Sim) swapModel(dyn *node, model string) []*node {
	slot := dyn.child("thr")
	if slot == nil {
		slot = dyn.child("exthr")
	}
	if slot == nil {
		return nil
	}
	delete(dyn.byName, slot.name)
	if model == dynModelExp {
		slot.name, slot.min, slot.max, slot.steps = "exthr", -80, 0, 161
	} else {
		slot.name, slot.min, slot.max, slot.steps = "thr", -60, 0, 121
	}
	slot.long = slot.name
	dyn.byName[slot.name] = slot
	slot.val = floatVal(quantize(slot, slot.val.f))
	return []*node{slot}
}

// convert turns a client token into a value of n (WG-F30, WG-F60..F65);
// ok is false for a token the parameter cannot take (VALUE ERROR).
func convert(n *node, tok token, o Options) (value, bool) {
	switch n.typ {
	case typeFader:
		return faderValue(tok, o)
	case typeLinFloat, typeLogFloat:
		return floatParam(n, tok)
	case typeInt:
		return intParam(n, tok)
	case typeStringEnum:
		return enumParam(n, tok)
	case typeFloatEnum:
		for _, it := range n.items {
			if tok.kind == tkFloat && it.val == tok.f {
				return floatVal(it.val), true
			}
		}
	case typeString:
		if tok.kind == tkText {
			t := string(tok.b)
			return textVal(t[:min(len(t), n.maxLen)]), true
		}
	}
	return value{}, false
}

// faderValue: dB as written, clamped to -144..+10 (WG-F60), optionally
// rounded (item 10). Raw D6 positions use the published top segment
// dB = 40 x raw - 30 (WG-F61); below -10 dB they are not documented and
// are refused.
func faderValue(tok token, o Options) (value, bool) {
	var db float64
	switch tok.kind {
	case tkFloat:
		db = float64(tok.f)
	case tkInt:
		db = float64(tok.i)
	case tkRaw:
		if tok.f < 0.5 || tok.f > 1 {
			return value{}, false
		}
		db = 40*float64(tok.f) - 30
	default:
		return value{}, false
	}
	if math.IsNaN(db) {
		return value{}, false
	}
	db = math.Min(math.Max(db, faderBottom), faderTop)
	if o.RoundFaderDB > 0 && db > faderBottom {
		db = math.Round(db/o.RoundFaderDB) * o.RoundFaderDB
	}
	return floatVal(float32(db)), true
}

func floatParam(n *node, tok token) (value, bool) {
	var v float64
	switch tok.kind {
	case tkFloat:
		v = float64(tok.f)
	case tkInt:
		v = float64(tok.i)
	case tkRaw:
		v = rawToValue(n, float64(tok.f))
	case tkStep:
		v = float64(n.val.f) + float64(tok.i)*float64(n.max-n.min)/float64(n.steps-1)
	default:
		return value{}, false
	}
	if math.IsNaN(v) {
		return value{}, false
	}
	return floatVal(quantize(n, float32(v))), true
}

func rawToValue(n *node, raw float64) float64 {
	raw = math.Min(math.Max(raw, 0), 1)
	if n.typ == typeLogFloat {
		return float64(n.min) * math.Pow(float64(n.max/n.min), raw)
	}
	return float64(n.min) + raw*float64(n.max-n.min)
}

// quantize clamps to the range and snaps to the law's steps: linear
// (WG-F63) or logarithmic (WG-F64).
func quantize(n *node, f float32) float32 {
	lo, hi, last := float64(n.min), float64(n.max), float64(n.steps-1)
	v := math.Min(math.Max(float64(f), lo), hi)
	if last <= 0 {
		return float32(v)
	}
	if n.typ == typeLogFloat && lo > 0 {
		k := math.Round(math.Log(v/lo) / math.Log(hi/lo) * last)
		return float32(lo * math.Pow(hi/lo, k/last))
	}
	k := math.Round((v - lo) / (hi - lo) * last)
	return float32(lo + k*(hi-lo)/last)
}

func intParam(n *node, tok token) (value, bool) {
	switch tok.kind {
	case tkInt:
		if tok.i >= n.imin && tok.i <= n.imax {
			return intVal(tok.i), true
		}
	case tkClick: // D8 toggles a 0/1 int (WG-F30)
		if n.imin == 0 && n.imax == 1 {
			return intVal(1 - n.val.i), true
		}
	case tkStep:
		return intVal(min(max(n.val.i+tok.i, n.imin), n.imax)), true
	}
	return value{}, false
}

// enumParam takes an item name, or a 0-based index (WG-F65).
func enumParam(n *node, tok token) (value, bool) {
	switch tok.kind {
	case tkText:
		for _, it := range n.items {
			if strings.EqualFold(it.name, string(tok.b)) {
				return textVal(it.name), true
			}
		}
	case tkInt:
		if tok.i >= 0 && int(tok.i) < len(n.items) {
			return textVal(n.items[tok.i].name), true
		}
	}
	return value{}, false
}

func tokenText(tok token) string {
	switch tok.kind {
	case tkFloat, tkRaw:
		return fmt.Sprintf("%g", tok.f)
	case tkText:
		return fmt.Sprintf("%q", tok.b)
	case tkClick:
		return "click"
	default:
		return fmt.Sprintf("%d", tok.i)
	}
}
