// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"fmt"
	"math"
	"slices"
)

// Headamp is the state of one headamp: the preamp of a physical input,
// shared by every channel that uses it (CR-23).
type Headamp struct {
	Index      int // as the driver numbers it (xair NN, x32 NNN + 1)
	GainDB     float64
	Phantom    bool // 48 V on; meaningful only with HasPhantom
	HasPhantom bool // the driver reads this headamp's phantom
}

// HeadampBank is where the console says an input is, for its own label.
type HeadampBank uint8

// Headamp banks.
const (
	BankLocal  HeadampBank = iota + 1 // x32 local XLR, xair mic
	BankAES50A                        // x32 AES50 port A stage box
	BankAES50B                        // x32 AES50 port B stage box
	BankAux                           // xair stereo aux
)

var bankNames = map[HeadampBank]string{BankLocal: "local", BankAES50A: "aes50a", BankAES50B: "aes50b", BankAux: "aux"}

func (b HeadampBank) String() string {
	if s, ok := bankNames[b]; ok {
		return s
	}
	return fmt.Sprintf("HeadampBank(%d)", b)
}

// HeadampInfo describes one headamp a session may report: its console
// label, its own Gain law and whether the driver writes it. A law the
// driver has not confirmed is read-only (never written with a guess).
type HeadampInfo struct {
	Index                int
	Bank                 HeadampBank
	Number               int // the console's own number within the bank, from 1
	MinDB, MaxDB, StepDB float64
	// WriteMaxDB is the highest value the driver writes, below MaxDB when
	// the wire maximum is not confirmed safe to write (xair.md XA-F116,
	// x32.md X32-F72: +59.5 on the mic law, +19.5 on the line law).
	WriteMaxDB float64
	Writable   bool
	HasPhantom bool
}

// Headamp returns the description of headamp index.
func (c Capabilities) Headamp(index int) (HeadampInfo, bool) {
	i, ok := slices.BinarySearchFunc(c.Headamps, index, func(h HeadampInfo, n int) int { return h.Index - n })
	if !ok {
		return HeadampInfo{}, false
	}
	return c.Headamps[i], true
}

// WithHeadamps returns a copy of s with exactly these headamps, sorted by
// index. Indexes are unique and >= 1, gains finite.
func (s State) WithHeadamps(hs []Headamp) (State, error) {
	out := slices.Clone(hs)
	slices.SortFunc(out, func(a, b Headamp) int { return a.Index - b.Index })
	for i, h := range out {
		if h.Index < 1 || (i > 0 && out[i-1].Index == h.Index) || math.IsNaN(h.GainDB) || math.IsInf(h.GainDB, 0) {
			return s, fmt.Errorf("headamp %d gain %v: %w", h.Index, h.GainDB, ErrInvalidState)
		}
	}
	s.headamps = out
	return s, nil
}

// Headamps returns a copy of the headamps, sorted by index.
func (s State) Headamps() []Headamp { return slices.Clone(s.headamps) }

func (s State) headampAt(index int) (int, bool) {
	return slices.BinarySearchFunc(s.headamps, index, func(h Headamp, n int) int { return h.Index - n })
}

func (s State) headampStrip(ref StripRef) (Strip, bool) {
	i, ok := s.headampAt(ref.Index)
	if !ok {
		return Strip{}, false
	}
	h := s.headamps[i]
	return Strip{Ref: ref, GainDB: h.GainDB, Phantom: h.Phantom, HasPhantom: h.HasPhantom}, true
}

// isHeadampField reports whether f is a field of headamp strips.
func isHeadampField(f Field) bool { return f == FieldGain || f == FieldPhantom }

// withHeadamp sets gain or phantom of a headamp in a copy of s. A gain of
// a headamp the state lacks adds it (x32: a patch change brought in an
// AES50 headamp); phantom needs a headamp that has it.
func (s State) withHeadamp(p ParamRef, v Value) (State, error) {
	if p.Strip.Kind != StripHeadamp || !isHeadampField(p.Field) || p.Strip.Index < 1 {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	i, ok := s.headampAt(p.Strip.Index)
	if p.Field == FieldPhantom {
		on, isBool := v.Bool()
		switch {
		case !isBool:
			return s, fmt.Errorf("%s needs a bool: %w", p, ErrInvalidValue)
		case !ok || !s.headamps[i].HasPhantom:
			return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
		}
		s.headamps = slices.Clone(s.headamps)
		s.headamps[i].Phantom = on
		return s, nil
	}
	db, isLevel := v.DB()
	if !isLevel || math.IsInf(db, 0) {
		return s, fmt.Errorf("%s needs a finite level: %w", p, ErrInvalidValue)
	}
	if !ok {
		s.headamps = slices.Insert(slices.Clone(s.headamps), i, Headamp{Index: p.Strip.Index, GainDB: db})
		return s, nil
	}
	s.headamps = slices.Clone(s.headamps)
	s.headamps[i].GainDB = db
	return s, nil
}
