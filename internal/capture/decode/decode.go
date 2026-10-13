// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package decode turns captured UDP datagrams and TCP byte streams into
// messages grouped by key: an OSC address, a MIDI NRPN number, a JSON path, a
// text line's address, or a binary signature. Decoders are plug-ins behind
// one interface; the flow layer picks one per flow by Detect score, or the
// operator forces one by name.
//
// Every decoder is a parser of untrusted bytes: bounds-checked, capped
// buffers, never a panic, and a fuzz target in this package.
package decode

import (
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// Kind is the family of a key.
type Kind string

// Key kinds.
const (
	KindOSC      Kind = "osc"
	KindNRPN     Kind = "nrpn"
	KindRPN      Kind = "rpn"
	KindCC       Kind = "cc"
	KindNote     Kind = "note"
	KindProgram  Kind = "program"
	KindPressure Kind = "pressure"
	KindPitch    Kind = "pitch"
	KindSysEx    Kind = "sysex"
	KindJSON     Kind = "json"
	KindText     Kind = "text"
	KindBinary   Kind = "bin"
)

// Key identifies one parameter-like thing in the traffic.
type Key struct {
	Kind Kind
	ID   string
}

// String is the key as shown to the operator.
func (k Key) String() string {
	switch k.Kind {
	case KindOSC, KindJSON, KindText:
		return k.ID
	case KindWing:
		return "wing " + k.ID
	case KindNRPN, KindRPN, KindCC, KindSysEx:
		return fmt.Sprintf("%s %s", upper(k.Kind), k.ID)
	default:
		return string(k.Kind) + " " + k.ID
	}
}

func upper(k Kind) string {
	b := []byte(k)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// Value is one observed value. Present is false for a message that carries
// no value (a read request such as an OSC address without arguments).
type Value struct {
	Text    string
	Num     float64
	HasNum  bool
	Present bool
}

// TextValue is a present, non-numeric value.
func TextValue(s string) Value { return Value{Text: s, Present: true} }

// NumValue is a present numeric value shown as text.
func NumValue(n float64, text string) Value {
	return Value{Text: text, Num: n, HasNum: true, Present: true}
}

// Equal reports whether two values are the same observation.
func (v Value) Equal(o Value) bool { return v.Present == o.Present && v.Text == o.Text }

// Message is one decoded message. Raw holds at most RawCap bytes of it.
type Message struct {
	Key   Key
	Value Value
	Raw   []byte
}

// RawCap bounds the evidence bytes kept per message.
const RawCap = 64

func newMessage(k Key, v Value, raw []byte) Message {
	return Message{Key: k, Value: v, Raw: slices.Clone(raw[:min(len(raw), RawCap)])}
}

// Stream decodes one direction of one flow. Feed takes a UDP datagram or the
// next in-order chunk of a TCP stream and calls emit for every complete
// message. Feed never retains chunk.
type Stream interface {
	Feed(chunk []byte, emit func(Message))
}

// Dropper is implemented by streams that can drop a message they cannot
// hold (too large or too deep); Dropped counts them so reports can say so.
type Dropper interface {
	Dropped() int
}

// Decoder is a protocol plug-in.
type Decoder interface {
	Name() string
	// Detect scores (0..100) how well samples match this decoder. For TCP,
	// samples are consecutive chunks of one direction of one stream.
	Detect(t netpkt.Transport, samples [][]byte) int
	// NewStream starts decoding one direction; sample is the start of that
	// direction's bytes when known (the binary decoder guesses framing from it).
	NewStream(t netpkt.Transport, sample []byte) Stream
}

// Options tune the decoders.
type Options struct {
	// BinaryKeyBytes is how many leading frame bytes form a binary key.
	BinaryKeyBytes int
	// SysExKeyBytes is how many bytes after F0 form a SysEx key.
	SysExKeyBytes int
}

// Defaults for Options.
const (
	DefaultBinaryKeyBytes = 4
	DefaultSysExKeyBytes  = 6
	maxKeyBytes           = 32
)

func (o Options) withDefaults() Options {
	if o.BinaryKeyBytes <= 0 {
		o.BinaryKeyBytes = DefaultBinaryKeyBytes
	}
	if o.SysExKeyBytes <= 0 {
		o.SysExKeyBytes = DefaultSysExKeyBytes
	}
	o.BinaryKeyBytes = min(o.BinaryKeyBytes, maxKeyBytes)
	o.SysExKeyBytes = min(o.SysExKeyBytes, maxKeyBytes)
	return o
}

// All returns every decoder, the generic binary one last.
func All(o Options) []Decoder {
	o = o.withDefaults()
	return []Decoder{OSC{}, WING{}, MIDI{opts: o}, Text{}, JSON{}, Binary{opts: o}}
}

// ByName returns the decoder called name.
func ByName(name string, o Options) (Decoder, error) {
	for _, d := range All(o) {
		if d.Name() == name {
			return d, nil
		}
	}
	return nil, fmt.Errorf("decode: no decoder %q (have osc, wing, midi, text, json, binary)", name)
}

// Choose returns the decoder with the highest score for samples, the first
// one on a tie; the binary decoder always scores above zero.
func Choose(decs []Decoder, t netpkt.Transport, samples ...[][]byte) Decoder {
	best, bestScore := decs[len(decs)-1], -1
	for _, d := range decs {
		s := 0
		for _, dir := range samples {
			s = max(s, d.Detect(t, dir))
		}
		if s > bestScore {
			best, bestScore = d, s
		}
	}
	return best
}

// hexCap renders b as hex, at most n bytes, noting the full length.
func hexCap(b []byte, n int) string {
	if len(b) <= n {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%s...(%d bytes)", hex.EncodeToString(b[:n]), len(b))
}

// buffer is a capped byte accumulator for stream decoders.
type buffer struct {
	b   []byte
	max int
}

// add appends chunk; it returns false (and empties the buffer) on overflow.
func (b *buffer) add(chunk []byte) bool {
	if len(b.b)+len(chunk) > b.max {
		b.b = b.b[:0]
		return false
	}
	b.b = append(b.b, chunk...)
	return true
}

// consume drops the first n bytes.
func (b *buffer) consume(n int) { b.b = append(b.b[:0], b.b[n:]...) }

// joined concatenates samples (detection of stream framings).
func joined(samples [][]byte) []byte {
	var out []byte
	for _, s := range samples {
		out = append(out, s...)
	}
	return out
}
