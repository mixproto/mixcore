// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"fmt"
	"strings"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// MIDI 1.0 facts used here (MIDI 1.0 Detailed Specification, MIDI
// Association): status bytes have the top bit set, data bytes do not;
// running status repeats the last channel status; 0xF8-0xFF are single-byte
// real-time messages that may appear anywhere; SysEx is F0 ... F7. NRPN
// select is CC 99 (MSB) and CC 98 (LSB), RPN select CC 101 and CC 100; data
// entry is CC 6 (MSB) and CC 38 (LSB).
const (
	midiStatusBit   = 0x80
	midiRealtime    = 0xF8
	midiSysExStart  = 0xF0
	midiSysExEnd    = 0xF7
	midiNoteOff     = 0x80
	midiNoteOn      = 0x90
	midiPolyAT      = 0xA0
	midiCC          = 0xB0
	midiProgram     = 0xC0
	midiChanAT      = 0xD0
	midiPitchBend   = 0xE0
	ccDataEntryMSB  = 6
	ccDataEntryLSB  = 38
	ccNRPNLSB       = 98
	ccNRPNMSB       = 99
	ccRPNLSB        = 100
	ccRPNMSB        = 101
	maxSysEx        = 64 << 10
	sysExValueShow  = 128
	scoreMIDI       = 80
	minMIDICoverage = 0.95
)

// MIDI decodes a raw MIDI byte stream (MIDI over TCP as some consoles speak
// it, or MIDI bytes in UDP datagrams). Keys: "NRPN ch1 0x0017" for data entry
// MSB after an NRPN select (the LSB is the separate key "... lsb"), "CC ch1
// #7", notes, program, pressure, pitch bend, and SysEx by its first bytes.
type MIDI struct{ opts Options }

// Name implements Decoder.
func (MIDI) Name() string { return "midi" }

// Detect implements Decoder: every byte must belong to a complete message
// and at least one channel or SysEx message must be present.
func (d MIDI) Detect(_ netpkt.Transport, samples [][]byte) int {
	all := joined(samples)
	if len(all) == 0 {
		return 0
	}
	st := d.newParser()
	n := 0
	st.feed(all, func(Message) { n++ })
	covered := st.inMessages + st.pending // a message cut at the end of the sample still counts
	if st.inSysEx {
		covered += len(st.sysex)
	}
	if n == 0 || float64(covered) < minMIDICoverage*float64(len(all)) {
		return 0
	}
	return scoreMIDI
}

// NewStream implements Decoder. UDP datagrams share one parser per direction,
// so running status may span datagrams.
func (d MIDI) NewStream(netpkt.Transport, []byte) Stream { return d.newParser() }

func (d MIDI) newParser() *midiParser {
	return &midiParser{sysexKey: d.opts.withDefaults().SysExKeyBytes}
}

type paramSelect struct {
	msb, lsb byte
	selected bool
	rpn      bool
}

type midiParser struct {
	status     byte
	data       [2]byte
	n, need    int
	pending    int // bytes of the message being assembled (for coverage)
	inSysEx    bool
	sysex      []byte
	sel        [16]paramSelect
	sysexKey   int
	inMessages int // bytes that ended up in complete messages
}

func (p *midiParser) Feed(chunk []byte, emit func(Message)) { p.feed(chunk, emit) }

func (p *midiParser) feed(b []byte, emit func(Message)) {
	for _, c := range b {
		switch {
		case c >= midiRealtime:
			p.inMessages++
		case c == midiSysExStart:
			p.inSysEx, p.sysex, p.status = true, append(p.sysex[:0], c), 0
		case c == midiSysExEnd && p.inSysEx:
			p.inSysEx = false
			p.emitSysEx(append(p.sysex, c), emit)
		case c&midiStatusBit != 0:
			p.inSysEx = false
			p.startStatus(c)
		case p.inSysEx:
			p.addSysEx(c)
		default:
			p.dataByte(c, emit)
		}
	}
}

func (p *midiParser) addSysEx(c byte) {
	if len(p.sysex) >= maxSysEx {
		p.inSysEx = false
		return
	}
	p.sysex = append(p.sysex, c)
}

func (p *midiParser) startStatus(c byte) {
	p.status, p.n, p.pending = c, 0, 1
	switch {
	case c < midiSysExStart:
		p.need = 2
		if hi := c & 0xF0; hi == midiProgram || hi == midiChanAT {
			p.need = 1
		}
	case c == 0xF1 || c == 0xF3: // time code quarter frame, song select
		p.need = 1
	case c == 0xF2: // song position
		p.need = 2
	default: // F4, F5, F6: no data
		p.inMessages++
		p.status = 0
	}
}

func (p *midiParser) dataByte(c byte, emit func(Message)) {
	if p.status == 0 {
		return // data without status: not in a message
	}
	// With running status the status byte is not repeated, so pending
	// counts only data bytes for this message.
	p.data[p.n] = c
	p.n++
	p.pending++
	if p.n < p.need {
		return
	}
	p.inMessages += p.pending
	p.n, p.pending = 0, 0
	if p.status >= midiSysExStart {
		p.status = 0 // system common has no running status
		return
	}
	p.channel(p.status, p.data, emit)
}

func (p *midiParser) channel(status byte, d [2]byte, emit func(Message)) {
	ch := int(status&0x0F) + 1
	raw := []byte{status, d[0], d[1]}
	switch status & 0xF0 {
	case midiCC:
		p.controlChange(ch, d[0], d[1], raw, emit)
	case midiNoteOn:
		emit(newMessage(Key{KindNote, fmt.Sprintf("ch%d %d", ch, d[0])}, num7(d[1]), raw))
	case midiNoteOff:
		emit(newMessage(Key{KindNote, fmt.Sprintf("ch%d %d", ch, d[0])}, num7(0), raw))
	case midiPolyAT:
		emit(newMessage(Key{KindPressure, fmt.Sprintf("ch%d note %d", ch, d[0])}, num7(d[1]), raw))
	case midiProgram:
		emit(newMessage(Key{KindProgram, fmt.Sprintf("ch%d", ch)}, num7(d[0]), raw[:2]))
	case midiChanAT:
		emit(newMessage(Key{KindPressure, fmt.Sprintf("ch%d", ch)}, num7(d[0]), raw[:2]))
	default: // pitch bend, 14 bits LSB first
		v := int(d[0]) | int(d[1])<<7
		emit(newMessage(Key{KindPitch, fmt.Sprintf("ch%d", ch)}, NumValue(float64(v), fmt.Sprintf("%d", v)), raw))
	}
}

func num7(v byte) Value { return NumValue(float64(v), fmt.Sprintf("0x%02X", v)) }

func (p *midiParser) controlChange(ch int, cc, v byte, raw []byte, emit func(Message)) {
	s := &p.sel[ch-1]
	switch cc {
	case ccNRPNMSB:
		*s = paramSelect{msb: v, lsb: s.lsb, selected: true}
	case ccNRPNLSB:
		*s = paramSelect{msb: s.msb, lsb: v, selected: true}
	case ccRPNMSB:
		*s = paramSelect{msb: v, lsb: s.lsb, selected: true, rpn: true}
	case ccRPNLSB:
		*s = paramSelect{msb: s.msb, lsb: v, selected: true, rpn: true}
	case ccDataEntryMSB, ccDataEntryLSB:
		if !s.selected {
			emit(newMessage(Key{KindCC, fmt.Sprintf("ch%d #%d", ch, cc)}, num7(v), raw))
			return
		}
		kind, id := KindNRPN, fmt.Sprintf("ch%d 0x%02X%02X", ch, s.msb, s.lsb)
		if s.rpn {
			kind = KindRPN
		}
		if cc == ccDataEntryLSB {
			id += " lsb"
		}
		emit(newMessage(Key{kind, id}, num7(v), raw))
	default:
		emit(newMessage(Key{KindCC, fmt.Sprintf("ch%d #%d", ch, cc)}, num7(v), raw))
	}
}

// emitSysEx keys a SysEx message by its first bytes after F0; the value is
// the rest before F7.
func (p *midiParser) emitSysEx(msg []byte, emit func(Message)) {
	p.inMessages += len(msg)
	body := msg[1 : len(msg)-1]
	k := min(p.sysexKey, len(body))
	parts := make([]string, 0, k+1)
	parts = append(parts, "F0")
	for _, c := range body[:k] {
		parts = append(parts, fmt.Sprintf("%02X", c))
	}
	key := Key{KindSysEx, strings.Join(parts, " ")}
	emit(newMessage(key, TextValue(hexCap(body[k:], sysExValueShow)), msg))
}
