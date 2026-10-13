// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"fmt"
	"math"
	"slices"
	"time"
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
	// StripBus is a mix bus or an FX send master (BusInfo.Kind): name,
	// colour, fader, mute, linked; inputs send to it (FieldSend*).
	StripBus
	// StripMatrix is a matrix output (x32): name, colour, fader, mute.
	StripMatrix
)

var stripKindNames = map[StripKind]string{
	StripInput: "input", StripMain: "main", StripMuteGroup: "muteGroup", StripDCA: "dca", StripHeadamp: "headamp",
	StripBus: "bus", StripMatrix: "matrix",
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
	// FieldColour is the strip colour the console shows (Shape.Colours).
	FieldColour
	// FieldLinked: the bus pair of an odd bus is linked (Shape.BusLinks).
	FieldLinked
	// FieldSendLevel, FieldSendOn, FieldSendPan and FieldSendTap are the
	// send of an input strip to bus ParamRef.Bus (Shape.Sends).
	FieldSendLevel
	FieldSendOn
	FieldSendPan
	FieldSendTap
	// FieldConsoleMute is the console's own report of an input or main
	// strip's effective mute (ConsoleMute* in mute.go). Internal: it feeds
	// EffectiveMute and is never a field clients name (ParseField refuses it).
	FieldConsoleMute
)

// consoleMuteName is the String of the internal FieldConsoleMute.
const consoleMuteName = "consoleMute"

var fieldNames = map[Field]string{
	FieldFader: "fader", FieldMute: "mute", FieldName: "name", FieldMuteGroups: "muteGroups", FieldDCAs: "dcas",
	FieldGain: "gain", FieldPhantom: "phantom", FieldHeadamp: "headamp", FieldColour: "colour", FieldLinked: "linked",
	FieldSendLevel: "sendLevel", FieldSendOn: "sendOn", FieldSendPan: "sendPan", FieldSendTap: "sendTap",
}

func (f Field) String() string {
	if f == FieldConsoleMute {
		return consoleMuteName
	}
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
	case FieldFader, FieldGain, FieldSendLevel:
		return KindLevel
	case FieldMute, FieldPhantom, FieldLinked, FieldSendOn:
		return KindBool
	case FieldSendPan:
		return KindPan
	case FieldSendTap:
		return KindTap
	case FieldColour:
		return KindColour
	case FieldHeadamp, FieldConsoleMute:
		return KindIndex
	case FieldName:
		return KindText
	case FieldMuteGroups, FieldDCAs:
		return KindMask
	default:
		return 0
	}
}

// ParamRef identifies one parameter: a strip and a field, and for a send
// field (Field.IsSend) the bus it sends to.
type ParamRef struct {
	Strip StripRef
	Field Field
	Bus   int // send fields only: the destination bus index; 0 otherwise
}

func (p ParamRef) String() string {
	if p.Bus != 0 {
		return fmt.Sprintf("%s %s bus %d", p.Strip, p.Field, p.Bus)
	}
	return p.Strip.String() + " " + p.Field.String()
}

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
	Headamp    int    // input strips: the headamp feeding it, 0 = none
	Colour     Colour // with Shape.Colours
	Linked     bool   // the odd bus of a linkable pair, with Shape.BusLinks
	// ConsoleMute is the console's own effective-mute report (ConsoleMute*).
	ConsoleMute int
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
	case FieldColour:
		v, err := ColourValue(s.Colour)
		return v, err == nil
	case FieldLinked:
		return Bool(s.Linked), s.Ref.Kind == StripBus
	case FieldConsoleMute:
		v, err := Index(s.ConsoleMute)
		return v, err == nil
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
	case FieldColour:
		s.Colour, _ = v.Colour()
	case FieldLinked:
		s.Linked, _ = v.Bool()
	case FieldConsoleMute:
		n, _ := v.Index()
		if n > consoleMuteMax {
			return s, fmt.Errorf("%s %s %d: %w", s.Ref, f, n, ErrInvalidValue)
		}
		s.ConsoleMute = n
	case FieldName:
		s.Name, _ = v.Text()
	default:
		return s, fmt.Errorf("%s %s: %w", s.Ref, f, ErrUnknownParam)
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
	dcaMasters []Strip   // name, fader, colour by DCA number - 1 (Shape.DCAMasters)
	headamps   []Headamp // sorted by index
	buses      []Strip   // sorted by index (bus.go)
	busInfos   []BusInfo // aligned with buses
	matrices   []Strip   // by matrix number - 1
	sends      [][]Send  // [input - 1][position in buses]; rows are replaced, never changed in place
	shape      Shape
	rules      MuteRules
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
		return s.groupStrip(ref)
	case ref.Kind == StripHeadamp:
		return s.headampStrip(ref)
	case ref.Kind == StripBus:
		i, ok := s.busAt(ref.Index)
		if !ok {
			return Strip{}, false
		}
		return s.buses[i], true
	case ref.Kind == StripMatrix && ref.Index >= 1 && ref.Index <= len(s.matrices):
		return s.matrices[ref.Index-1], true
	default:
		return Strip{}, false
	}
}

// Value returns the current value of p.
func (s State) Value(p ParamRef) (Value, bool) {
	if p.Field.IsSend() {
		return s.sendValue(p)
	}
	st, ok := s.Strip(p.Strip)
	if !ok || !s.has(p) {
		return Value{}, false
	}
	return st.Value(p.Field)
}

// has reports whether p (not a send) exists in this state's shape.
func (s State) has(p ParamRef) bool {
	k := p.Strip.Kind
	switch {
	case p.Bus != 0 || (k == StripHeadamp) != isHeadampField(p.Field):
		return false
	case p.Field == FieldColour:
		return s.shape.Colours && (k != StripDCA || s.shape.DCAMasters) && k != StripMuteGroup
	case p.Field == FieldLinked:
		return k == StripBus && s.hasLink(p.Strip.Index)
	case k == StripMuteGroup:
		return p.Field == FieldMute
	case k == StripDCA:
		return p.Field == FieldMute || (s.shape.DCAMasters && (p.Field == FieldName || p.Field == FieldFader))
	case k == StripBus || k == StripMatrix:
		return p.Field == FieldName || p.Field == FieldFader || p.Field == FieldMute
	}
	return true
}

// With returns a new State with p set to v. The receiver is not modified.
func (s State) With(p ParamRef, v Value) (State, error) {
	if p.Field.IsSend() {
		return s.withSend(p, v)
	}
	if !s.has(p) && p.Strip.Kind != StripHeadamp && !isHeadampField(p.Field) {
		return s, fmt.Errorf("%s: %w", p, ErrUnknownParam)
	}
	switch p.Strip.Kind {
	case StripMuteGroup, StripDCA:
		return s.withGroup(p, v)
	case StripBus, StripMatrix:
		return s.withOutput(p, v)
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
	MainMuteGroups bool
	// MuteGroupMembershipUnknown: the console does not report which mute
	// groups a strip belongs to (wing, item 11); strips carry none and the
	// state's MuteRules say so.
	MuteGroupMembershipUnknown bool
	InputMeters                bool
	EchoesOwnWrites            bool
	MaxPushClients             int
	// Buses that answered, sorted by index (core-api v0.4); the state
	// carries the same list (State.BusInfos) and which send fields exist
	// (State.Shape).
	Buses []BusInfo
	// Matrices is the number of read-only matrices (x32 6, X32-F87).
	Matrices int
	// FaderSteps and SendLevelSteps are the console's grids of the fader
	// law: valid positions are k/steps (x32 X32-F41, X32-F83: 1023 and 160;
	// xair XA-F41 the same). 0: unknown, values compare exactly.
	FaderSteps, SendLevelSteps int
	// EchoWindow is how long the driver matches its own writes against the
	// console's pushes (xosc Timing.EchoWindow): a write older than that
	// can no longer come back as its echo. 0: unknown.
	EchoWindow time.Duration
	// TapLabels are the console's own words for the taps TapInput..TapGroup
	// (index tap - 1), when the console reports send taps (X32-F85,
	// xair.md 3.3); shown untranslated (CR-16).
	TapLabels [TapCount]string
}
