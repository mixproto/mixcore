// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"bytes"
	"os"
	"testing"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/osc"
)

// fuzzDecoder feeds b split at cut to a UDP and a TCP stream of d, plus
// Detect: nothing may panic, and every message stays within its caps.
func fuzzDecoder(t *testing.T, d Decoder, b []byte, cut uint16) {
	at := int(cut) % (len(b) + 1)
	chunks := [][]byte{b[:at], b[at:]}
	_ = d.Detect(netpkt.UDP, chunks)
	_ = d.Detect(netpkt.TCP, chunks)
	for _, tr := range []netpkt.Transport{netpkt.UDP, netpkt.TCP} {
		s := d.NewStream(tr, b)
		for _, c := range chunks {
			s.Feed(c, func(m Message) {
				if len(m.Raw) > RawCap {
					t.Fatalf("%s: raw evidence of %d bytes", d.Name(), len(m.Raw))
				}
				if m.Key.Kind == "" {
					t.Fatalf("%s: message without a key kind", d.Name())
				}
			})
		}
	}
}

func FuzzOSCStream(f *testing.F) {
	f.Add(oscPacket(f, "/ch/01/mix/fader", osc.Float(0.75)), uint16(3))
	f.Add([]byte("#bundle\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x08/a\x00\x00,\x00\x00\x00"), uint16(9))
	f.Add([]byte{0, 0, 0, 8, '/', 'a', 0, 0, ',', 0, 0, 0}, uint16(2))
	f.Fuzz(func(t *testing.T, b []byte, cut uint16) { fuzzDecoder(t, OSC{}, b, cut) })
}

func FuzzMIDIStream(f *testing.F) {
	f.Add(bytes.Join(hexChunks(f, "testdata/midi-nrpn.hex"), nil), uint16(5))
	f.Add([]byte{0xF0, 0x00, 0xB0, 0x63, 0xF7, 0x06, 0x01}, uint16(1))
	d := MIDI{opts: Options{}.withDefaults()}
	f.Fuzz(func(t *testing.T, b []byte, cut uint16) { fuzzDecoder(t, d, b, cut) })
}

func FuzzTextStream(f *testing.F) {
	lines, _ := os.ReadFile("testdata/json-lines.txt")
	f.Add(lines, uint16(10))
	f.Add([]byte("OK get MIXER:Current/InCh/Fader/Level 0 0 -32768\nset \"unterminated\n"), uint16(4))
	f.Fuzz(func(t *testing.T, b []byte, cut uint16) { fuzzDecoder(t, Text{}, b, cut) })
}

func FuzzJSONStream(f *testing.F) {
	f.Add([]byte("UC\x00\x01{\"path\":\"a\",\"value\":[1,{\"b\":null}]}junk{\"x\":\"\\\"}\"}"), uint16(7))
	f.Add(bytes.Repeat([]byte("{["), 40), uint16(0))
	f.Fuzz(func(t *testing.T, b []byte, cut uint16) { fuzzDecoder(t, JSON{}, b, cut) })
}

func FuzzBinaryStream(f *testing.F) {
	f.Add([]byte{0, 4, 0xA5, 1, 0, 0x10, 0, 4, 0xA5, 1, 0, 0x11, 0, 4, 0xA5, 1, 0, 0x12}, uint16(7))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0}, uint16(3))
	d := Binary{opts: Options{}.withDefaults()}
	f.Fuzz(func(t *testing.T, b []byte, cut uint16) {
		if fr, ok := GuessFraming(b); ok && (fr.Width == 0 || fr.Offset > 4) {
			t.Fatalf("GuessFraming returned an impossible framing %+v", fr)
		}
		fuzzDecoder(t, d, b, cut)
	})
}
