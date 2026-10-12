// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"context"
	"sync/atomic"

	"github.com/mixproto/mixcore/model"
)

// headampSet records which headamps the session has: read and reported.
// The owner goroutine changes it (connect, a patch change); Apply reads it
// from any goroutine.
type headampSet struct{ on []atomic.Bool }

func newHeadampSet(maxIndex int) headampSet { return headampSet{on: make([]atomic.Bool, maxIndex+1)} }

func (h *headampSet) has(i int) bool { return i > 0 && i < len(h.on) && h.on[i].Load() }

func (h *headampSet) set(i int, on bool) {
	if i > 0 && i < len(h.on) {
		h.on[i].Store(on)
	}
}

// unitGroup is the layout's group of probed units (at most one).
func (l Layout) unitGroup() (HeadampGroup, bool) {
	for _, g := range l.Headamps {
		if g.Role == HeadampUnit {
			return g, true
		}
	}
	return HeadampGroup{}, false
}

// markStatic marks the units that answered (s.headamps of them, from the
// group's first index) and, with s.extra, the extra groups.
func (s *session) markStatic() {
	for _, g := range s.params.layout.Headamps {
		for h := g.First; h < g.First+g.Count; h++ {
			switch g.Role {
			case HeadampUnit:
				s.present.set(h, h-g.First < s.headamps)
			case HeadampExtra:
				s.present.set(h, s.extra)
			}
		}
	}
}

// probedHeadamps is the number of units below the first one that did not
// answer (xair: 16 on 18-channel models, 8 on XR16, 4 on XR12, XA-F73).
func (s *session) probedHeadamps(rs *readState) int {
	g, ok := s.params.layout.unitGroup()
	if !ok {
		return 0
	}
	n := g.Count
	for i, p := range s.params.all {
		if p.role == HeadampUnit && p.ref.Field == model.FieldGain && rs.gaveUp[i] {
			n = min(n, p.headamp-g.First)
		}
	}
	if n < g.Count {
		s.log.Info(s.name+": headamps did not answer; Gain absent for them until the engine is restarted", "headamps", n)
	}
	return n
}

// hasFullUnits reports whether every unit answered and the layout has an
// extra group (xair aux 17 on the 18-channel models, XA-F110).
func (s *session) hasFullUnits() bool {
	g, ok := s.params.layout.unitGroup()
	if !ok || s.headamps < g.Count {
		return false
	}
	for _, e := range s.params.layout.Headamps {
		if e.Role == HeadampExtra {
			return true
		}
	}
	return false
}

// dropUnansweredExtras clears s.extra when an extra headamp's read gave up.
func (s *session) dropUnansweredExtras(rs *readState) {
	for i, p := range s.params.all {
		if p.role == HeadampExtra && rs.gaveUp[i] && s.extra {
			s.extra = false
			s.log.Info(s.name+": extra headamp did not answer; absent for this engine", "addr", p.addr)
		}
	}
	s.markStatic()
}

// patchedTarget returns the headamp an index value points at when it is a
// patched one (x32 AES50).
func (s *session) patchedTarget(v model.Value) (int, bool) {
	n, ok := v.Index()
	if !ok || n == 0 {
		return 0, false
	}
	g, ok := s.params.layout.headampGroup(n)
	return n, ok && g.Role == HeadampPatched
}

// readPatched reads the gain of every patched headamp an input's /-ha
// index points at, after the rest of the initial read (X32-F74). One that
// does not answer stays absent.
func (s *session) readPatched(ctx context.Context, rs *readState) error {
	var order []int
	for i, p := range s.params.all {
		if p.conv != convHaIndex || !rs.have[i] || rs.gaveUp[i] {
			continue
		}
		if h, ok := s.patchedTarget(rs.values[i]); ok && !s.present.has(h) {
			s.present.set(h, true)
			gi := s.params.index(pref(model.StripRef{Kind: model.StripHeadamp, Index: h}, model.FieldGain))
			rs.probe[gi] = true
			order = append(order, gi)
		}
	}
	if len(order) == 0 {
		return nil
	}
	if err := s.readPass(ctx, rs, order); err != nil {
		return err
	}
	for _, gi := range order {
		if rs.gaveUp[gi] {
			s.present.set(s.params.all[gi].headamp, false)
			s.log.Info(s.name+": patched headamp did not answer; absent", "addr", s.params.all[gi].addr)
		}
	}
	return nil
}

// onHeadampIndex follows a patch change while live: an index that now
// points at a patched headamp the session lacks reads that one headamp
// (never all of them). Its reply arrives as a console change.
func (s *session) onHeadampIndex(v model.Value) {
	h, ok := s.patchedTarget(v)
	if !ok || s.present.has(h) {
		return
	}
	s.present.set(h, true)
	p, _ := s.params.byRef(pref(model.StripRef{Kind: model.StripHeadamp, Index: h}, model.FieldGain))
	s.log.Debug(s.name+": a channel now uses a patched headamp; reading it", "addr", p.addr)
	s.sendSub(p.addr) // a read, never a write (X32-F04)
}

// headampInfos describes every headamp the session may report: present
// units and extras, and every patched one (a patch change may bring it in).
func (s *session) headampInfos() []model.HeadampInfo {
	var out []model.HeadampInfo
	for _, g := range s.params.layout.Headamps {
		for h := g.First; h < g.First+g.Count; h++ {
			if g.Role == HeadampPatched || s.present.has(h) {
				info := g.info(h)
				info.HasPhantom = info.HasPhantom && !s.absent[featurePhantom]
				out = append(out, info)
			}
		}
	}
	return out
}

// stateHeadamps lists the present headamps for the snapshot.
func (s *session) stateHeadamps() []model.Headamp {
	var out []model.Headamp
	for _, g := range s.params.layout.Headamps {
		for h := g.First; h < g.First+g.Count; h++ {
			if s.present.has(h) {
				out = append(out, model.Headamp{Index: h, HasPhantom: g.Phantom && !s.absent[featurePhantom]})
			}
		}
	}
	return out
}
