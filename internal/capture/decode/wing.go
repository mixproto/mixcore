// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/wingproto"
)

// KindWing keys a WING native-protocol message: the selected node ("#<hash>"
// after D7, or a path built from DA and node names) (docs/protocols/wing.md
// WG-F30, WG-F31), or "discovery".
const KindWing Kind = "wing"

// WING decoder limits and scores.
const (
	scoreWingSelect = 95 // a stream that starts with DF D1 or DF D3 (WG-F14, WG-F15)
	scoreWingTokens = 70 // a stream with several D7 <hash> <value|DC|DD> groups
	scoreWingUDP    = 95 // a discovery datagram (WG-F11)
	wingMinGroups   = 3
	wingMaxBuffer   = wingproto.MaxDefLen + 64<<10
	wingDiscovery   = "WING?"
	wingIdentity    = "WING,"
	wingMeterShow   = 128
)

// WING decodes the native protocol on TCP 2222: the 0xDF channel mux, then
// the data channel's tokens. Each value is keyed by the node selected before
// it, so a capture of the vendor app maps hashes to controls; reads (DC) and
// definition requests (DD) are keyed with no value, node definitions (DF) by
// their own hash with name, type and range as the value, and the meter
// channel's requests (DF D3) as "meters". UDP: discovery datagrams only;
// meter datagrams go to a port the app chooses and are left to the binary
// decoder, which keys them by report id and length (WG-F50..F52).
type WING struct{}

// Name implements Decoder.
func (WING) Name() string { return "wing" }

// Detect implements Decoder.
func (WING) Detect(t netpkt.Transport, samples [][]byte) int {
	if t == netpkt.UDP {
		for _, s := range samples {
			if string(s) == wingDiscovery || bytes.HasPrefix(s, []byte(wingIdentity)) {
				return scoreWingUDP
			}
		}
		return 0
	}
	b := joined(samples)
	if bytes.HasPrefix(b, []byte{wingproto.Escape, 0xD1}) || bytes.HasPrefix(b, []byte{wingproto.Escape, 0xD3}) {
		return scoreWingSelect
	}
	if groups, tokens := wingGroups(b); groups >= wingMinGroups && 4*groups >= tokens {
		return scoreWingTokens // at least half the tokens are D7 groups: not random bytes
	}
	return 0
}

// wingGroups counts D7 <hash> tokens followed by a value, DC or DD, and all tokens.
func wingGroups(b []byte) (groups, tokens int) {
	var d wingproto.Demux
	data := d.Append(nil, b, wingproto.ChannelData)
	afterHash := false
	for len(data) > 0 {
		tok, used, err := wingproto.Parse(data)
		if err != nil {
			break
		}
		data = data[used:]
		tokens++
		switch {
		case tok.Kind == wingproto.KindHash:
			afterHash = true
			continue
		case afterHash && tok.Kind != wingproto.KindNodeIndex && tok.Kind != wingproto.KindNodeName:
			groups++
		}
		afterHash = false
	}
	return groups, tokens
}

// NewStream implements Decoder.
func (WING) NewStream(t netpkt.Transport, _ []byte) Stream {
	if t == netpkt.UDP {
		return wingUDP{}
	}
	return &wingStream{}
}

type wingUDP struct{}

func (wingUDP) Feed(chunk []byte, emit func(Message)) {
	switch {
	case string(chunk) == wingDiscovery:
		emit(newMessage(Key{KindWing, "discovery"}, Value{}, chunk))
	case bytes.HasPrefix(chunk, []byte(wingIdentity)):
		emit(newMessage(Key{KindWing, "discovery"}, TextValue(strings.ToValidUTF8(string(chunk[:min(len(chunk), binaryValueShow)]), "?")), chunk))
	}
}

// wingStream decodes one direction of a TCP stream. Two demuxers see every
// byte: one keeps the data channel, the other the meter channel.
type wingStream struct {
	data, meter       wingproto.Demux
	dataBuf, meterBuf []byte
	sel               string
	dropped           int
}

// Dropped implements Dropper.
func (s *wingStream) Dropped() int { return s.dropped }

func (s *wingStream) Feed(chunk []byte, emit func(Message)) {
	s.dataBuf = s.data.Append(s.dataBuf, chunk, wingproto.ChannelData)
	s.meterBuf = s.meter.Append(s.meterBuf, chunk, wingproto.ChannelMeters)
	used := 0
	for used < len(s.dataBuf) {
		tok, n, err := wingproto.Parse(s.dataBuf[used:])
		if errors.Is(err, wingproto.ErrShort) {
			break
		}
		if err != nil { // an absurd definition length: the rest cannot be framed
			s.dropped++
			used = len(s.dataBuf)
			break
		}
		s.token(tok, s.dataBuf[used:used+n], emit)
		used += n
	}
	s.dataBuf = append(s.dataBuf[:0], s.dataBuf[used:]...)
	if len(s.dataBuf) > wingMaxBuffer {
		s.dropped++
		s.dataBuf = s.dataBuf[:0]
	}
	s.meters(emit)
}

func (s *wingStream) token(tok wingproto.Token, raw []byte, emit func(Message)) {
	switch tok.Kind {
	case wingproto.KindHash:
		s.sel = fmt.Sprintf("#%08x", tok.Hash)
	case wingproto.KindRoot:
		s.sel = "/"
	case wingproto.KindNodeName:
		s.sel = strings.TrimSuffix(s.sel, "/") + "/" + strings.ToValidUTF8(string(tok.Bytes), "?")
	case wingproto.KindNodeIndex:
		s.sel = strings.TrimSuffix(s.sel, "/") + "/" + strconv.Itoa(int(tok.Int))
	case wingproto.KindUp:
		if i := strings.LastIndexByte(s.sel, '/'); i > 0 {
			s.sel = s.sel[:i]
		}
	case wingproto.KindDataRequest:
		emit(newMessage(Key{KindWing, s.sel + " read"}, Value{}, raw))
	case wingproto.KindDefRequest:
		emit(newMessage(Key{KindWing, s.sel + " defs"}, Value{}, raw))
	case wingproto.KindDef:
		emitDef(tok.Bytes, raw, emit)
	case wingproto.KindEnd:
	default:
		emit(newMessage(Key{KindWing, s.sel}, wingValue(tok), raw))
	}
}

// wingValue renders a value token: numbers keep their number for the
// analyzer's law fit (WG-F30: D5 is in the parameter's unit).
func wingValue(tok wingproto.Token) Value {
	switch tok.Kind {
	case wingproto.KindInt:
		return NumValue(float64(tok.Int), strconv.Itoa(int(tok.Int)))
	case wingproto.KindFloat:
		return NumValue(float64(tok.Float), strconv.FormatFloat(float64(tok.Float), 'g', 7, 32))
	case wingproto.KindRawFloat:
		return NumValue(float64(tok.Float), "raw "+strconv.FormatFloat(float64(tok.Float), 'g', 7, 32))
	case wingproto.KindString:
		return TextValue(strconv.Quote(strings.ToValidUTF8(string(tok.Bytes), "?")))
	case wingproto.KindClick:
		return TextValue("click (toggle)")
	case wingproto.KindStep:
		return NumValue(float64(tok.Int), "step "+strconv.Itoa(int(tok.Int)))
	}
	return TextValue("?")
}

func emitDef(body, raw []byte, emit func(Message)) {
	d, err := wingproto.ParseDef(body)
	if err != nil {
		emit(newMessage(Key{KindWing, "def ?"}, TextValue("malformed definition"), raw))
		return
	}
	v := fmt.Sprintf("name=%s parent=#%08x index=%d type=%d unit=%d ro=%v", strings.ToValidUTF8(d.Name, "?"), d.Parent, d.Index, d.Type, d.Unit, d.ReadOnly)
	switch d.Type {
	case wingproto.TypeLinFloat, wingproto.TypeLogFloat:
		v += fmt.Sprintf(" range=%g..%g steps=%d", d.Min, d.Max, d.Steps)
	case wingproto.TypeInt:
		v += fmt.Sprintf(" range=%d..%d", d.IntMin, d.IntMax)
	case wingproto.TypeStringEnum, wingproto.TypeFloatEnum:
		v += fmt.Sprintf(" items=%d", len(d.Items))
	}
	emit(newMessage(Key{KindWing, fmt.Sprintf("def #%08x", d.Hash)}, TextValue(v), raw))
}

// meters emits each meter-channel request at its end (DE) or a renewal (D4).
func (s *wingStream) meters(emit func(Message)) {
	if len(s.meterBuf) == 0 {
		return
	}
	end := bytes.IndexByte(s.meterBuf, 0xDE)
	if end < 0 && len(s.meterBuf) < 5 {
		return
	}
	if end < 0 {
		end = len(s.meterBuf) - 1
	}
	req := s.meterBuf[:end+1]
	emit(newMessage(Key{KindWing, "meters"}, TextValue(hexCap(req, wingMeterShow)), req))
	s.meterBuf = append(s.meterBuf[:0], s.meterBuf[end+1:]...)
}
