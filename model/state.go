// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"fmt"
	"math"
	"slices"
)

// StripKind is the kind of a channel strip.
type StripKind uint8

// Strip kinds supported so far. More kinds (bus, matrix) arrive with the
// drivers that need them. Mute groups and DCAs are group strips: their only
// field is FieldMute (a mute group: engaged; a DCA: muted).
const (
	StripInput StripKind = iota + 1
	StripMain
	StripMuteGroup
	StripDCA
	// StripHeadamp is a preamp (the physical input), numbered by the
	// driver; its fields are FieldGain and FieldPhantom (CR-23).
	StripHeadamp
)

var stripKindNames = map[StripKind]string{
	StripInput: "input", StripMain: "main", StripMuteGroup: "muteGroup", StripDCA: "dca", StripHeadamp: "headamp",
}

func (k StripKind) String() string {
	if s, ok := stripKindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("StripKind(%d)", k)
}

// ParseStripKind parses the String form of a strip kind.
func ParseStripKind(s string) (StripKind, error) {
	for k, name := range stripKindNames {
		if name == s {
			return k, nil
		}
	}
	return 0, fmt.Errorf("strip kind %q: %w", s, ErrUnknownParam)
}

// StripRef identifies a strip. Inputs are numbered from 1; the main strip has Index 0.
type StripRef struct {
	Kind  StripKind
	Index int
}

// MainRef is the main stereo (LR) strip.
var MainRef = StripRef{Kind: StripMain}

func (r StripRef) String() string {
	if r.Kind == StripMain {
		return "main"
	}
	return fmt.Sprintf("%s %d", r.Kind, r.Index)
}

// Field is a parameter of a strip.
type Field uint8

// Fields supported so far. FieldMuteGroups and FieldDCAs are the group
// membership of an input or main strip.
const (
	FieldFader Field = iota + 1
	FieldMute
	FieldName
	FieldMuteGroups
	FieldDCAs
	// FieldGain is the gain of a headamp strip in dB.
	FieldGain
	// FieldPhantom is the 48 V of a headamp strip (true = on).
	FieldPhantom
	// FieldHeadamp is the headamp feeding an input strip (an index, 0 = none).
	FieldHeadamp
)

var fieldNames = map[Field]string{
	FieldFader: "fader", FieldMute: "mute", FieldName: "name", FieldMuteGroups: "muteGroups", FieldDCAs: "dcas",
	FieldGain: "gain", FieldPhantom: "phantom", FieldHeadamp: "headamp",
}

func (f Field) String() string {
	if s, ok := fieldNames[f]; ok {
		return s
	}
	return fmt.Sprintf("Field(%d)", f)
}

// ParseField parses the String form of a field.
func ParseField(s string) (Field, error) {
	for f, name := range fieldNames {
		if name == s {
			return f, nil
		}
	}
	return 0, fmt.Errorf("field %q: %w", s, ErrUnknownParam)
}

// ValueKind returns the kind of value the field holds (0 for unknown fields).
func (f Field) ValueKind() ValueKind {
	switch f {
	case FieldFader, FieldGain:
		return KindLevel
	case FieldMute, FieldPhantom:
		return KindBool
	case FieldHeadamp:
		return KindIndex
	case FieldName:
		return KindText
	case FieldMuteGroups, FieldDCAs:
		return KindMask
	default:
		return 0
	}
}

// ParamRef identifies one parameter: a strip and a field.
type ParamRef struct {
	Strip StripRef
	Field Field
}

func (p ParamRef) String() string { return p.Strip.String() + " " + p.Field.String() }

// Strip is the state of one channel strip in engineering units.
type Strip struct {
	Ref        StripRef
	Name       string
	FaderDB    float64 // -inf = off
	Muted      bool
	MuteGroups uint32  // membership: bit n-1 = mute group n
	DCAs       uint32  // membership: bit d-1 = DCA d
	GainDB     float64 // headamp strips only
	Phantom    bool    // headamp strips with HasPhantom only
	HasPhantom bool
	Headamp    int // input strips: the headamp feeding it, 0 = none
}

// Value returns the value of field f.
func (s Strip) Value(f Field) (Value, bool) {
	switch f {
	case FieldFader:
		v, err := Level(s.FaderDB)
		return v, err == nil
	case FieldMute:
		return Bool(s.Muted), true
	case FieldName:
		v, err := Text(s.Name)
		return v, err == nil
	case FieldMuteGroups:
		return Mask(s.MuteGroups), true
	case FieldDCAs:
		return Mask(s.DCAs), true
	case FieldGain:
		v, err := Level(s.GainDB)
		return v, err == nil && s.Ref.Kind == StripHeadamp
	case FieldPhantom:
		return Bool(s.Phantom), s.Ref.Kind == StripHeadamp && s.HasPhantom
	case FieldHeadamp:
		v, err := Index(s.Headamp)
		return v, err == nil && s.Ref.Kind == StripInput
	default:
		return Value{}, false
	}
}

// with returns a copy of s with field f set to v.
func (s Strip) with(f Field, v Value) (Strip, error) {
	if f.ValueKind() == 0 {
		return s, fmt.Errorf("%s: %w", f, ErrUnknownParam)
	}
	if v.Kind() != f.ValueKind() {
		return s, fmt.Errorf("%s needs value kind %d, got %d: %w", f, f.ValueKind(), v.Kind(), ErrInvalidValue)
	}
	switch f {
	case FieldFader:
		s.FaderDB, _ = v.DB()
	case FieldMute:
		s.Muted, _ = v.Bool()
	case FieldMuteGroups:
		s.MuteGroups, _ = v.Mask()
	case FieldDCAs:
		s.DCAs, _ = v.Mask()
	case FieldHeadamp:
		if s.Ref.Kind != StripInput {
			return s, fmt.Errorf("%s %s: %w", s.Ref, f, ErrUnknownParam)
		}
		s.Headamp, _ = v.Index()
	default:
		s.Name, _ = v.Text()
	}
	return s, nil
}

func (s Strip) validate() error {
	if math.IsNaN(s.FaderDB) || math.IsInf(s.FaderDB, 1) {
		return fmt.Errorf("%s fader %v: %w", s.Ref, s.FaderDB, ErrInvalidState)
	}
	return nil
}

// State is an immutable snapshot of the console. Mutating methods return a new
// State; accessors return copies.
type State struct {
	inputs     []Strip
	main       Strip
	hasMain    bool
	muteGroups []bool    // engaged, by group number - 1
	dcaMutes   []bool    // muted, by DCA number - 1
	headamps   []Headamp // sorted by index
}

// NewState builds a state from input strips numbered 1..n in order and an
// optional main strip. The slices are copied.
func NewState(inputs []Strip, main *Strip) (State, error) {
	for i, in := range inputs {
		if in.Ref != (StripRef{Kind: StripInput, Index: i + 1}) {
			return State{}, fmt.Errorf("input %d has ref %s: %w", i+1, in.Ref, ErrInvalidState)
		}
		if err := in.validate(); err != nil {
			return State{}, err
		}
	}
	s := State{inputs: slices.Clone(inputs)}
	if main != nil {
		if main.Ref != MainRef {
			return State{}, fmt.Errorf("main has ref %s: %w", main.Ref, ErrInvalidState)
		}
		if err := main.validate(); err != nil {
			return State{}, err
		}
		s.main, s.hasMain = *main, true
	}
	return s, nil
}

// Inputs returns a copy of the input strips.
func (s State) Inputs() []Strip { return slices.Clone(s.inputs) }

// Main returns the main strip if the console has one.
func (s State) Main() (Strip, bool) { return s.main, s.hasMain }

// Strips returns all strips: inputs first, then main.
func (s State) Strips() []Strip {
	out := slices.Clone(s.inputs)
	if s.hasMain {
		out = append(out, s.main)
	}
	return out
}

// Strip returns the strip with the given ref. A group strip carries only Ref
// and Muted.
func (s State) Strip(ref StripRef) (Strip, bool) {
	switch {
	case ref.Kind == StripInput && ref.Index >= 1 && ref.Index <= len(s.inputs):
		return s.inputs[ref.Index-1], true
	case ref == MainRef && s.hasMain:
		return s.main, true
	case ref.Kind == StripMuteGroup || ref.Kind == StripDCA:
		on, ok := groupAt(s.groupList(ref.Kind), ref.Index)
		return Strip{Ref: ref, Muted: on}, ok
	case ref.Kind == StripHeadamp:
		return s.headampStrip(ref)
	default:
		return Strip{}, false
	}
}

// Value returns the current value of p.
func (s State) Value(p ParamRef) (Value, bool) {
	st, ok := s.Strip(p.Strip)
	if !ok || (p.Strip.isGroup() && p.Field != FieldMute) || (p.Strip.Kind == StripHeadamp) != isHeadampField(p.Field) {
		return Value{}, false
	}
	return st.Value(p.Field)
}

// With returns a new State with p set to v. The receiver is not modified.
func (s State) With(p ParamRef, v Value) (State, error) {
	if p.Strip.isGroup() {
		return s.withGroup(p, v)
	}
	if p.Strip.Kind == StripHeadamp || isHeadampField(p.Field) {
		return s.withHeadamp(p, v)
	}
	st, ok := s.Strip(p.Strip)
	if !ok {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	updated, err := st.with(p.Field, v)
	if err != nil {
		return s, err
	}
	if p.Strip == MainRef {
		s.main = updated
		return s, nil
	}
	s.inputs = slices.Clone(s.inputs)
	s.inputs[p.Strip.Index-1] = updated
	return s, nil
}

// Capabilities describes what a connected console offers. A feature the
// console lacks is absent (zero), never faked.
type Capabilities struct {
	// Headamps describes every headamp the session may report, sorted by
	// index, each with its own Gain law (CR-23). The state holds the ones
	// read so far (x32: AES50 headamps only once a channel uses them).
	Headamps []HeadampInfo
	// InputHeadamps: input strips carry FieldHeadamp (x32 /-ha, X32-F74).
	InputHeadamps bool
	InputCount    int
	// AbsentInputs is how many input strips the family documents above
	// InputCount that this console did not answer for (xair: XA-F72).
	AbsentInputs int
	HasMain      bool
	MuteGroups   int
	DCAs         int
	// MainMuteGroups: the main strip has a mute-group membership (x32 yes,
	// xair no). Without it the main's MuteGroups field is not modelled.
	MainMuteGroups  bool
	InputMeters     bool
	EchoesOwnWrites bool
	MaxPushClients  int
}
