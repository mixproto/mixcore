// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"fmt"
	"math"
	"slices"
)

// Buses, matrices and sends (core-api v0.4). A state is shaped once by the
// driver (WithBuses, WithMatrices, WithShape) and then only changes values.
// Sends are kept as one row per input strip, aligned with the bus list, so
// a change copies one row (a few buses), never every send of the console.

// WithBuses returns a copy of s with exactly these buses, sorted by index,
// each at -inf and unmuted, and a send row per input strip at -inf, off,
// centred, tap TapInput (the drivers then set every value they read).
// Indexes are unique and >= 1.
func (s State) WithBuses(infos []BusInfo) (State, error) {
	out := slices.Clone(infos)
	slices.SortFunc(out, func(a, b BusInfo) int { return a.Index - b.Index })
	for i, b := range out {
		if b.Index < 1 || (i > 0 && out[i-1].Index == b.Index) {
			return s, fmt.Errorf("bus %d: %w", b.Index, ErrInvalidState)
		}
	}
	s.busInfos = out
	s.buses = make([]Strip, len(out))
	for i, b := range out {
		s.buses[i] = Strip{Ref: StripRef{Kind: StripBus, Index: b.Index}, FaderDB: math.Inf(-1)}
	}
	s.sends = make([][]Send, len(s.inputs))
	for in := range s.sends {
		row := make([]Send, len(out))
		for i, b := range out {
			row[i] = Send{Bus: b.Index, LevelDB: math.Inf(-1), Tap: TapInput} // tap 0 on the wire of both families
		}
		s.sends[in] = row
	}
	return s, nil
}

// WithMatrices returns a copy of s with matrices 1..n at -inf, unmuted.
func (s State) WithMatrices(n int) State {
	s.matrices = make([]Strip, max(n, 0))
	for i := range s.matrices {
		s.matrices[i] = Strip{Ref: StripRef{Kind: StripMatrix, Index: i + 1}, FaderDB: math.Inf(-1)}
	}
	return s
}

// WithShape returns a copy of s that carries the optional parameters of sh.
func (s State) WithShape(sh Shape) State {
	s.shape = sh
	if sh.DCAMasters && len(s.dcaMasters) != len(s.dcaMutes) {
		s.dcaMasters = make([]Strip, len(s.dcaMutes))
		for i := range s.dcaMasters {
			s.dcaMasters[i] = Strip{Ref: StripRef{Kind: StripDCA, Index: i + 1}, FaderDB: math.Inf(-1)}
		}
	}
	return s
}

// Shape returns the optional parameters this state carries.
func (s State) Shape() Shape { return s.shape }

// Buses returns a copy of the bus strips, sorted by index.
func (s State) Buses() []Strip { return slices.Clone(s.buses) }

// BusInfos returns a copy of the bus descriptions, sorted by index.
func (s State) BusInfos() []BusInfo { return slices.Clone(s.busInfos) }

// Matrices returns a copy of the matrix strips in order.
func (s State) Matrices() []Strip { return slices.Clone(s.matrices) }

// Sends returns a copy of the sends of input strip input (1-based), in bus
// order; nil for an unknown input.
func (s State) Sends(input int) []Send {
	if input < 1 || input > len(s.sends) {
		return nil
	}
	return slices.Clone(s.sends[input-1])
}

// busAt returns the position of bus index in s.buses.
func (s State) busAt(index int) (int, bool) {
	return slices.BinarySearchFunc(s.buses, index, func(b Strip, n int) int { return b.Ref.Index - n })
}

// hasLink: bus index is the odd bus of a linkable pair whose even bus the
// state has too, and the console reports bus links.
func (s State) hasLink(index int) bool {
	i, ok := s.busAt(index)
	if !ok || !s.shape.BusLinks || index%2 == 0 || !s.busInfos[i].Linkable {
		return false
	}
	j, ok := s.busAt(index + 1)
	return ok && s.busInfos[j].Linkable
}

// sendPos finds the send of p: input row and bus position. ok is false for
// a field the shape lacks, an unknown input or an absent bus.
func (s State) sendPos(p ParamRef) (row, pos int, ok bool) {
	if p.Strip.Kind != StripInput || p.Strip.Index < 1 || p.Strip.Index > len(s.sends) || !s.hasSendField(p) {
		return 0, 0, false
	}
	pos, ok = s.busAt(p.Bus)
	return p.Strip.Index - 1, pos, ok
}

// hasSendField: the shape has this send field for this bus. Pair-scoped
// pan and tap exist on the odd bus of a linkable pair only.
func (s State) hasSendField(p ParamRef) bool {
	sh := s.shape.Sends
	switch p.Field {
	case FieldSendLevel:
		return sh.Level
	case FieldSendOn:
		return sh.On
	case FieldSendPan, FieldSendTap:
		i, ok := s.busAt(p.Bus)
		has := ok && ((p.Field == FieldSendPan && sh.Pan && s.busInfos[i].SendPan) || (p.Field == FieldSendTap && sh.Tap))
		if !has || !sh.PairScoped || p.Bus%2 == 1 {
			return has
		}
		return !s.busInfos[i].Linkable // a bus outside the pairs has its own pan and tap
	}
	return false
}

func (s State) sendValue(p ParamRef) (Value, bool) {
	row, pos, ok := s.sendPos(p)
	if !ok {
		return Value{}, false
	}
	v := s.sends[row][pos].value(p.Field)
	return v, v.Kind() != 0
}

// withSend sets one send field in a copy of s; only that input's row is copied.
func (s State) withSend(p ParamRef, v Value) (State, error) {
	row, pos, ok := s.sendPos(p)
	if !ok {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	if v.Kind() != p.Field.ValueKind() {
		return s, fmt.Errorf("%s needs value kind %d, got %d: %w", p, p.Field.ValueKind(), v.Kind(), ErrInvalidValue)
	}
	r := slices.Clone(s.sends[row])
	r[pos] = r[pos].with(p.Field, v)
	s.sends = slices.Clone(s.sends)
	s.sends[row] = r
	return s, nil
}

// withOutput sets a field of a bus or matrix strip in a copy of s.
func (s State) withOutput(p ParamRef, v Value) (State, error) {
	st, ok := s.Strip(p.Strip)
	if !ok {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	updated, err := st.with(p.Field, v)
	if err != nil {
		return s, err
	}
	if err := updated.validate(); err != nil {
		return s, err
	}
	if p.Strip.Kind == StripMatrix {
		s.matrices = slices.Clone(s.matrices)
		s.matrices[p.Strip.Index-1] = updated
		return s, nil
	}
	i, _ := s.busAt(p.Strip.Index)
	s.buses = slices.Clone(s.buses)
	s.buses[i] = updated
	return s, nil
}
