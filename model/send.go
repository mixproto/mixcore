// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import "fmt"

// Tap is where a send takes its signal from the channel, in signal-flow
// order. Both modelled families have these six (x32.md X32-F85, xair.md
// section 3.3); each names them in its own words (Capabilities.TapLabels).
type Tap uint8

// Send taps.
const (
	TapInput     Tap = iota + 1 // after the input stage (x32 "IN/LC", xair "IN")
	TapPreEQ                    // before the EQ
	TapPostEQ                   // after the EQ
	TapPreFader                 // before the channel fader: a monitor mix
	TapPostFader                // after the channel fader
	TapGroup                    // the bus works as a subgroup
)

// TapCount is the number of taps; taps are 1..TapCount.
const TapCount = int(TapGroup)

var tapNames = [...]string{"", "input", "preEq", "postEq", "preFader", "postFader", "group"}

func (t Tap) valid() bool { return t >= TapInput && t <= TapGroup }

func (t Tap) String() string {
	if t.valid() {
		return tapNames[t]
	}
	return fmt.Sprintf("Tap(%d)", t)
}

// ParseTap parses the String form of a tap.
func ParseTap(s string) (Tap, error) {
	for t := TapInput; t <= TapGroup; t++ {
		if tapNames[t] == s {
			return t, nil
		}
	}
	return 0, fmt.Errorf("tap %q: %w", s, ErrInvalidValue)
}

// Colour is a console strip colour: eight hues, then the same eight drawn
// inverted (x32.md X32-F89, xair.md section 3.2).
type Colour uint8

// ColourCount is the number of colours; colours are 0..ColourCount-1.
const ColourCount Colour = 16

var colourNames = [ColourCount]string{
	"off", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"offInverted", "redInverted", "greenInverted", "yellowInverted", "blueInverted", "magentaInverted", "cyanInverted", "whiteInverted",
}

func (c Colour) String() string {
	if c < ColourCount {
		return colourNames[c]
	}
	return fmt.Sprintf("Colour(%d)", c)
}

// ParseColour parses the String form of a colour.
func ParseColour(s string) (Colour, error) {
	for c, name := range colourNames {
		if name == s {
			return Colour(c), nil
		}
	}
	return 0, fmt.Errorf("colour %q: %w", s, ErrInvalidValue)
}

// BusKind says what a bus is for.
type BusKind uint8

// Bus kinds.
const (
	BusMix BusKind = iota + 1 // a mix (aux) bus: monitor mixes
	BusFX                     // an FX send master (xair FX 1..4 as buses 7..10)
)

func (k BusKind) String() string {
	switch k {
	case BusMix:
		return "mix"
	case BusFX:
		return "fx"
	default:
		return fmt.Sprintf("BusKind(%d)", k)
	}
}

// BusInfo describes one bus a session has.
type BusInfo struct {
	Index int
	Kind  BusKind
	// Linkable: the bus belongs to an odd/even pair the console can link
	// as one stereo bus (x32.md X32-F81, xair.md section 3.2).
	Linkable bool
}

// Send is the send of one input strip to one bus, in engineering units.
// Which fields exist is the state's SendShape.
type Send struct {
	Bus     int
	LevelDB float64 // -inf = off
	On      bool    // true = the send is on
	Pan     float64 // -100..+100
	Tap     Tap
}

// SendShape says which send fields a console has.
type SendShape struct {
	Level, On, Pan, Tap bool
	// PairScoped: pan and tap exist on the odd bus of each linkable pair
	// only and belong to the pair (x32.md X32-F84, X32-F85).
	PairScoped bool
}

// Shape says which optional parameters a state carries. A console that
// lacks one has it absent, never faked.
type Shape struct {
	Colours    bool // inputs, main, buses, matrices (and DCAs with DCAMasters) carry FieldColour
	DCAMasters bool // DCA strips carry FieldName and FieldFader (and FieldColour with Colours)
	BusLinks   bool // the odd bus of each linkable pair carries FieldLinked
	Sends      SendShape
}

// IsSend reports whether f is a field of a send (ParamRef.Bus names the bus).
func (f Field) IsSend() bool {
	return f == FieldSendLevel || f == FieldSendOn || f == FieldSendPan || f == FieldSendTap
}

// value returns field f of the send.
func (s Send) value(f Field) Value {
	switch f {
	case FieldSendOn:
		return Bool(s.On)
	case FieldSendPan:
		v, _ := Pan(s.Pan)
		return v
	case FieldSendTap:
		v, _ := TapValue(s.Tap)
		return v
	default:
		return Value{kind: KindLevel, db: s.LevelDB}
	}
}

// with returns a copy of s with field f set from v (kind checked by the caller).
func (s Send) with(f Field, v Value) Send {
	switch f {
	case FieldSendOn:
		s.On, _ = v.Bool()
	case FieldSendPan:
		s.Pan, _ = v.Pan()
	case FieldSendTap:
		s.Tap, _ = v.Tap()
	default:
		s.LevelDB, _ = v.DB()
	}
	return s
}
