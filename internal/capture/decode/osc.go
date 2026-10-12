// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/osc"
)

// OSC limits and constants (OSC 1.0 spec,
// https://opensoundcontrol.stanford.edu/spec-1_0.html: bundles are
// "#bundle", an 8-byte time tag and int32-size-prefixed elements; over a
// stream transport every packet is preceded by its int32 size).
const (
	oscBundleTag    = "#bundle\x00"
	oscBundleHdr    = 16
	oscMaxDepth     = 4
	oscMaxPacket    = 65536
	oscStreamBuffer = 1 << 20
	oscBlobShow     = 64
	scoreOSCUDP     = 90
	scoreOSCTCP     = 85
)

// OSC decodes OSC 1.0 messages and bundles over UDP, and size-prefixed OSC
// packets over TCP. Key: the address. A message without arguments (a read
// request in most console dialects) has no value.
type OSC struct{}

// Name implements Decoder.
func (OSC) Name() string { return "osc" }

// Detect implements Decoder.
func (OSC) Detect(t netpkt.Transport, samples [][]byte) int {
	if len(samples) == 0 {
		return 0
	}
	if t == netpkt.TCP {
		n, ok := 0, true
		forEachSized(joined(samples), func(p []byte) {
			n++
			ok = ok && validOSC(p, 0)
		})
		if n > 0 && ok {
			return scoreOSCTCP
		}
		return 0
	}
	good := 0
	for _, s := range samples {
		if validOSC(s, 0) {
			good++
		}
	}
	return scoreOSCUDP * good / len(samples)
}

// NewStream implements Decoder.
func (OSC) NewStream(t netpkt.Transport, _ []byte) Stream {
	if t == netpkt.TCP {
		return &oscTCP{buf: buffer{max: oscStreamBuffer}}
	}
	return oscUDP{}
}

func validOSC(p []byte, depth int) bool {
	ok := true
	n := 0
	walkOSC(p, depth, func(m *osc.Message, raw []byte, err error) {
		n++
		ok = ok && err == nil
	})
	return ok && n > 0
}

type oscUDP struct{}

func (oscUDP) Feed(chunk []byte, emit func(Message)) { emitOSC(chunk, emit) }

func emitOSC(p []byte, emit func(Message)) {
	walkOSC(p, 0, func(m *osc.Message, raw []byte, err error) {
		if err != nil {
			emit(newMessage(Key{KindOSC, "(undecodable)"}, TextValue(hexCap(raw, oscBlobShow)), raw))
			return
		}
		emit(newMessage(Key{KindOSC, string(m.Address)}, oscValue(m), raw))
	})
}

// walkOSC calls fn for every message in p, descending into bundles.
func walkOSC(p []byte, depth int, fn func(m *osc.Message, raw []byte, err error)) {
	if !bytes.HasPrefix(p, []byte(oscBundleTag)) {
		var m osc.Message
		err := osc.Decode(p, &m)
		fn(&m, p, err)
		return
	}
	if depth >= oscMaxDepth || len(p) < oscBundleHdr {
		fn(nil, p, osc.ErrTruncated)
		return
	}
	rest := p[oscBundleHdr:]
	for len(rest) > 0 {
		if len(rest) < 4 {
			fn(nil, rest, osc.ErrTruncated)
			return
		}
		n := int(binary.BigEndian.Uint32(rest))
		if n <= 0 || n%4 != 0 || n > len(rest)-4 {
			fn(nil, rest, osc.ErrTruncated)
			return
		}
		walkOSC(rest[4:4+n], depth+1, fn)
		rest = rest[4+n:]
	}
}

// oscValue renders the arguments; the first numeric argument is the number.
func oscValue(m *osc.Message) Value {
	if m.Tagless {
		if len(m.Untagged) == 0 {
			return Value{}
		}
		return TextValue("untagged " + hexCap(m.Untagged, oscBlobShow))
	}
	args := m.Args()
	if len(args) == 0 {
		return Value{}
	}
	parts := make([]string, 0, len(args))
	v := Value{Present: true}
	for i, a := range args {
		switch a.Type {
		case osc.TypeInt:
			parts = append(parts, strconv.FormatInt(int64(a.Int), 10))
			v.setFirstNum(i, float64(a.Int))
		case osc.TypeFloat:
			parts = append(parts, strconv.FormatFloat(float64(a.Float), 'g', -1, 32))
			v.setFirstNum(i, float64(a.Float))
		case osc.TypeString:
			parts = append(parts, strconv.Quote(string(a.Bytes)))
		default:
			parts = append(parts, "blob:"+hexCap(a.Bytes, oscBlobShow))
		}
	}
	v.Text = strings.Join(parts, " ")
	return v
}

func (v *Value) setFirstNum(i int, n float64) {
	if i == 0 {
		v.Num, v.HasNum = n, true
	}
}

// oscTCP splits a stream into int32-size-prefixed OSC packets.
type oscTCP struct{ buf buffer }

func (s *oscTCP) Feed(chunk []byte, emit func(Message)) {
	if !s.buf.add(chunk) {
		return
	}
	used := forEachSized(s.buf.b, func(p []byte) { emitOSC(p, emit) })
	if used < 0 {
		s.buf.b = s.buf.b[:0] // a corrupt size: resynchronize on the next chunk
		return
	}
	s.buf.consume(used)
}

// forEachSized calls fn for each complete size-prefixed packet in b and
// returns the bytes used, or -1 when a size is impossible.
func forEachSized(b []byte, fn func([]byte)) int {
	used := 0
	for len(b)-used >= 4 {
		n := int(binary.BigEndian.Uint32(b[used:]))
		if n <= 0 || n > oscMaxPacket {
			return -1
		}
		if len(b)-used-4 < n {
			break
		}
		fn(b[used+4 : used+4+n])
		used += 4 + n
	}
	return used
}
