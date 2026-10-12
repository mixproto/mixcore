// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package analyze

import (
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
)

// KeyValue is one harvested parameter.
type KeyValue struct {
	Key      decode.Key
	Value    decode.Value
	Evidence flow.Evidence // where the harvest first saw it
}

// Harvest is the initial-sync burst after an app endpoint first appears:
// every key that carried a value until no new key arrived for HarvestQuiet.
type Harvest struct {
	ID     int
	App    netip.AddrPort
	Start  time.Time
	End    time.Time
	Closed bool
	Keys   []KeyValue // sorted by key when closed; values as of End
	seen   map[decode.Key]int
	lastNw time.Time
}

func (a *Analyzer) openHarvest() *Harvest {
	if len(a.harvests) == 0 {
		return nil
	}
	h := a.harvests[len(a.harvests)-1]
	if h.Closed {
		return nil
	}
	return h
}

// harvestStart opens a harvest when a new app endpoint appears and none is open.
func (a *Analyzer) harvestStart(o flow.Observation) {
	if a.openHarvest() != nil {
		return
	}
	h := &Harvest{ID: len(a.harvests) + 1, App: o.App, Start: o.At, lastNw: o.At, seen: map[decode.Key]int{}}
	a.harvests = append(a.harvests, h)
}

func (a *Analyzer) harvestSee(o flow.Observation) {
	h := a.openHarvest()
	if h == nil {
		return
	}
	if _, ok := h.seen[o.Msg.Key]; ok {
		return
	}
	h.seen[o.Msg.Key] = len(h.Keys)
	h.Keys = append(h.Keys, KeyValue{Key: o.Msg.Key, Value: o.Msg.Value, Evidence: o.Evidence})
	h.lastNw = o.At
}

func (a *Analyzer) closeHarvest(now time.Time) {
	h := a.openHarvest()
	if h == nil || now.Sub(h.lastNw) <= a.cfg.HarvestQuiet {
		return
	}
	a.finishHarvest(h)
}

func (a *Analyzer) finishHarvest(h *Harvest) {
	h.Closed, h.End = true, h.lastNw
	for i := range h.Keys {
		if v, ok := a.Value(h.Keys[i].Key); ok {
			h.Keys[i].Value = v
		}
	}
	slices.SortFunc(h.Keys, func(x, y KeyValue) int { return compareKeys(x.Key, y.Key) })
	h.seen = nil
	a.cfg.Notify(HarvestDone{Harvest: h})
}

// Harvests returns the harvests so far.
func (a *Analyzer) Harvests() []*Harvest { return slices.Clone(a.harvests) }

// Finish closes every open mark and harvest (end of a recording).
func (a *Analyzer) Finish() {
	for _, m := range a.marks {
		a.closeMark(m)
	}
	if h := a.openHarvest(); h != nil {
		a.finishHarvest(h)
	}
}
