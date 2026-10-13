// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/osc"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// got is a decoded message in a comparable form.
type got struct{ key, val string }

func (g got) String() string { return g.key + " = " + g.val }

func feedAll(s Stream, chunks [][]byte) []got {
	var out []got
	for _, c := range chunks {
		s.Feed(c, func(m Message) {
			v := m.Value.Text
			if !m.Value.Present {
				v = "<none>"
			}
			out = append(out, got{m.Key.String(), v})
		})
	}
	return out
}

func wantMessages(t *testing.T, have []got, want ...string) {
	t.Helper()
	var hs []string
	for _, h := range have {
		hs = append(hs, h.String())
	}
	if strings.Join(hs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(hs, "\n"), strings.Join(want, "\n"))
	}
}

// hexChunks reads a fixture: one chunk of hex bytes per line, '#' comments.
func hexChunks(t testing.TB, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: a fixture path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		h := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
		if h == "" {
			continue
		}
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, b)
	}
	return out
}

func oscPacket(t testing.TB, addr string, args ...osc.Arg) []byte {
	t.Helper()
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOSC_UDP(t *testing.T) {
	bundle := []byte("#bundle\x00\x00\x00\x00\x00\x00\x00\x00\x01")
	inner := oscPacket(t, "/ch/02/mix/on", osc.Int(0))
	bundle = fmt.Appendf(bundle, "%c%c%c%c", 0, 0, 0, len(inner))
	bundle = append(bundle, inner...)
	s := OSC{}.NewStream(netpkt.UDP, nil)
	wantMessages(t, feedAll(s, [][]byte{
		oscPacket(t, "/ch/01/mix/fader"),
		oscPacket(t, "/ch/01/mix/fader", osc.Float(0.75)),
		oscPacket(t, "/ch/01/config/name", osc.String("Vox")),
		oscPacket(t, "/meters/1", osc.Blob([]byte{1, 2})),
		bundle,
		[]byte("garbage!"),
	}),
		"/ch/01/mix/fader = <none>",
		"/ch/01/mix/fader = 0.75",
		`/ch/01/config/name = "Vox"`,
		"/meters/1 = blob:0102",
		"/ch/02/mix/on = 0",
		"(undecodable) = 6761726261676521",
	)
}

func TestOSC_TCPSizePrefixed(t *testing.T) {
	p := oscPacket(t, "/a", osc.Int(7))
	framed := append([]byte{0, 0, 0, byte(len(p))}, p...)
	stream := append(bytes.Clone(framed), framed...)
	s := OSC{}.NewStream(netpkt.TCP, nil)
	wantMessages(t, feedAll(s, [][]byte{stream[:5], stream[5:20], stream[20:]}), "/a = 7", "/a = 7")
	if (OSC{}).Detect(netpkt.TCP, [][]byte{stream}) == 0 {
		t.Fatal("size-prefixed OSC over TCP not detected")
	}
}

func TestMIDIFixture(t *testing.T) {
	chunks := hexChunks(t, "testdata/midi-nrpn.hex")
	d := MIDI{opts: Options{}.withDefaults()}
	wantMessages(t, feedAll(d.NewStream(netpkt.TCP, nil), chunks),
		"NRPN ch1 0x0017 = 0x5A",
		"NRPN ch1 0x0017 lsb = 0x03",
		"NRPN ch1 0x0017 = 0x5F",
		"CC ch2 #7 = 0x64",
		"SYSEX F0 00 00 1A 50 10 01 = 00027f",
		"program ch3 = 0x05",
	)
	if s := d.Detect(netpkt.TCP, chunks); s != scoreMIDI {
		t.Fatalf("Detect = %d, want %d", s, scoreMIDI)
	}
}

func TestMIDI_OtherMessages(t *testing.T) {
	d := MIDI{opts: Options{}.withDefaults()}
	in := [][]byte{{0x90, 60, 100, 60, 0, 0x80, 61, 5, 0xE0, 0x00, 0x40, 0xD0, 9, 0xA0, 60, 3, 0xB0, 101, 0, 100, 0, 6, 2, 0xB0, 6, 1}}
	wantMessages(t, feedAll(d.NewStream(netpkt.UDP, nil), in),
		"note ch1 60 = 0x64", "note ch1 60 = 0x00", "note ch1 61 = 0x00",
		"pitch ch1 = 8192", "pressure ch1 = 0x09", "pressure ch1 note 60 = 0x03", "RPN ch1 0x0000 = 0x02",
		"RPN ch1 0x0000 = 0x01")
}

func TestMIDI_DetectToleratesACutMessage(t *testing.T) {
	d := MIDI{opts: Options{}.withDefaults()}
	chunks := hexChunks(t, "testdata/midi-nrpn.hex")[:3] // the third chunk ends inside a message
	if s := d.Detect(netpkt.TCP, chunks); s != scoreMIDI {
		t.Fatalf("Detect = %d on a sample cut inside a message", s)
	}
}

func TestMIDI_DetectRejectsText(t *testing.T) {
	d := MIDI{opts: Options{}.withDefaults()}
	if s := d.Detect(netpkt.TCP, [][]byte{[]byte("set MIXER:Current/InCh/Fader/Level 0 0 -1000\n")}); s != 0 {
		t.Fatalf("MIDI scored %d on text", s)
	}
}

func TestJSONLinesFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/json-lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	s := Text{}.NewStream(netpkt.TCP, nil)
	wantMessages(t, feedAll(s, [][]byte{b[:30], b[30:]}),
		"line/ch1/volume = 0.5", `$.id = "Set"`,
		"$.channels[0].fader = -10", "$.channels[0].mute = false",
		"$.channels[1].fader = 0", "$.channels[1].mute = true", `$.name = "Main"`,
		"line/ch2/mute = 1",
		"line/ch1/volume = 0.75", `$.id = "Set"`,
	)
}

func TestJSONFramedInBinary(t *testing.T) {
	var stream []byte
	for _, obj := range []string{`{"path":"a/b","value":1}`, `{"path":"a/b","value":2}`} {
		stream = append(stream, 'U', 'C', 0, 1, byte(len(obj)), 0)
		stream = append(stream, obj...)
	}
	if (Text{}).Detect(netpkt.TCP, [][]byte{stream}) != 0 {
		t.Fatal("text decoder claimed a binary-framed stream")
	}
	if (JSON{}).Detect(netpkt.TCP, [][]byte{stream}) == 0 {
		t.Fatal("embedded JSON not detected")
	}
	s := JSON{}.NewStream(netpkt.TCP, nil)
	wantMessages(t, feedAll(s, [][]byte{stream[:20], stream[20:]}), "a/b = 1", "a/b = 2")
}

func TestTextKeyRule(t *testing.T) {
	tests := []struct{ line, want string }{
		{"set MIXER:Current/InCh/Fader/Level 0 0 -1000", "MIXER:Current/InCh/Fader/Level 0 0 = -1000"},
		{"OK set MIXER:Current/InCh/Fader/Level 0 0 -1000", "MIXER:Current/InCh/Fader/Level 0 0 = -1000"},
		{"NOTIFY set MIXER:Current/InCh/Fader/Level 0 0 -500", "MIXER:Current/InCh/Fader/Level 0 0 = -500"},
		{"get MIXER:Current/InCh/Fader/Level 0 0", "MIXER:Current/InCh/Fader/Level 0 0 = <none>"},
		{"OK get MIXER:Current/InCh/Fader/Level 0 0 -32768", "MIXER:Current/InCh/Fader/Level 0 0 = -32768"},
		{`set MIXER:Current/InCh/Label/Name 0 0 "Lead Vox"`, `MIXER:Current/InCh/Label/Name 0 0 = "Lead Vox"`},
		{"volume 12", "volume = 12"},
		{"ping", "ping = <none>"},
	}
	for _, tc := range tests {
		have := feedAll(Text{}.NewStream(netpkt.UDP, nil), [][]byte{[]byte(tc.line)})
		if len(have) != 1 || have[0].String() != tc.want {
			t.Errorf("%q -> %v, want %q", tc.line, have, tc.want)
		}
	}
}

func TestBinaryFraming(t *testing.T) {
	// u16be length of the bytes after the field, then a 2-byte type and a value.
	frames := [][]byte{
		{0, 4, 0xA5, 0x01, 0x00, 0x10},
		{0, 4, 0xA5, 0x01, 0x00, 0x11},
		{0, 6, 0xA5, 0x02, 1, 2, 3, 4},
		{0, 4, 0xA5, 0x01, 0x00, 0x12},
	}
	stream := bytes.Join(frames, nil)
	f, ok := GuessFraming(stream)
	if !ok || f != (Framing{Offset: 0, Width: 2}) {
		t.Fatalf("GuessFraming = %v, %v; want u16be@0", f, ok)
	}
	d := Binary{opts: Options{}.withDefaults()}
	s := d.NewStream(netpkt.TCP, stream)
	wantMessages(t, feedAll(s, [][]byte{stream[:7], stream[7:]}),
		"bin 0004a501/6 = 0010", "bin 0004a501/6 = 0011", "bin 0006a502/8 = 01020304", "bin 0004a501/6 = 0012")
	if fr, framed := FramingOf(s); !framed || fr.String() != "u16be@0 (bytes after the field)" {
		t.Fatalf("FramingOf = %v %v", fr, framed)
	}
}

func TestBinaryNoFramingIsPerChunk(t *testing.T) {
	d := Binary{opts: Options{BinaryKeyBytes: 2}.withDefaults()}
	s := d.NewStream(netpkt.TCP, []byte{0xff, 0xff, 0xff})
	wantMessages(t, feedAll(s, [][]byte{{1, 2, 3}, {9}}), "bin 0102/3 = 03", "bin 09/1 = ")
}

func TestChoose(t *testing.T) {
	decs := All(Options{})
	tests := []struct {
		name    string
		tr      netpkt.Transport
		samples [][]byte
		want    string
	}{
		{"osc", netpkt.UDP, [][]byte{oscPacket(t, "/xinfo"), oscPacket(t, "/ch/01/mix/fader", osc.Float(1))}, "osc"},
		{"text", netpkt.TCP, [][]byte{[]byte("devinfo productname\n")}, "text"},
		{"midi", netpkt.TCP, hexChunks(t, "testdata/midi-nrpn.hex"), "midi"},
		{"binary", netpkt.UDP, [][]byte{{0xde, 0xad, 0xbe, 0xef}}, "binary"},
	}
	for _, tc := range tests {
		if d := Choose(decs, tc.tr, tc.samples); d.Name() != tc.want {
			t.Errorf("%s: chose %s, want %s", tc.name, d.Name(), tc.want)
		}
	}
	if _, err := ByName("nope", Options{}); err == nil {
		t.Fatal("ByName accepted an unknown decoder")
	}
}

// Regression (go-reviewer M1): an object that never closes was rescanned from
// its start on every chunk (quadratic: 128 KiB in 1-byte segments took 20 s).
// The scan state now survives feeds and a pending object is capped, so the
// stream stays linear and recovers for the next object. Linearity is proven
// by counting scan steps, not by wall-clock time: the old rescan needed about
// n*n/2 steps here, the linear scan exactly one per byte, under any load.
func TestJSONUnclosedObjectIsLinearAndCapped(t *testing.T) {
	const quadratic = 128 << 10 // one byte per segment: 11 s with the old rescan
	stream := append([]byte(`{"a":"`), bytes.Repeat([]byte("x"), maxPendingJSON+quadratic)...)
	s := JSON{}.NewStream(netpkt.TCP, nil)
	for i := range quadratic {
		s.Feed(stream[i:i+1], func(Message) {})
	}
	s.Feed(stream[quadratic:], func(Message) {}) // the rest at once, past the cap
	if got := scanSteps(t, s); got != len(stream) {
		t.Fatalf("scanned %d steps for %d bytes; every byte must be looked at exactly once", got, len(stream))
	}
	// The oversized object was dropped and counted; a later object still decodes.
	got := feedAll(s, [][]byte{[]byte(`"} {"path":"a/b","value":3}`)})
	if len(got) != 1 || got[0].String() != "a/b = 3" {
		t.Fatalf("after the dropped object: %d messages, first %.80s", len(got), got)
	}
	if n := s.(Dropper).Dropped(); n != 1 {
		t.Fatalf("Dropped() = %d, want 1", n)
	}
}

// The scan cost does not depend on how the stream is cut into chunks: an
// unclosed object, a closed one and bytes between objects, fed whole, in
// 1-byte and in 7-byte segments, take one step per byte every time.
func TestJSONScanStepsAreIndependentOfChunking(t *testing.T) {
	// Two closed objects (one leaf each; braces and an escaped quote inside a
	// string; nesting), then one that never closes.
	stream := []byte(`noise {"path":"a/b","value":"}\"{"} more {"c":[{"d":1}]} tail {"x":"` + strings.Repeat("y", 4096))
	for _, seg := range []int{len(stream), 1, 7} {
		s := JSON{}.NewStream(netpkt.TCP, nil)
		messages := 0
		for i := 0; i < len(stream); i += seg {
			s.Feed(stream[i:min(i+seg, len(stream))], func(Message) { messages++ })
		}
		if got := scanSteps(t, s); got != len(stream) || messages != 2 {
			t.Errorf("segments of %d: %d steps for %d bytes, %d messages (want 2)", seg, got, len(stream), messages)
		}
	}
}

// scanSteps returns how many bytes the stream's object scanner examined.
func scanSteps(t *testing.T, s Stream) int {
	t.Helper()
	js, ok := s.(*jsonStream)
	if !ok {
		t.Fatalf("stream is %T, want *jsonStream", s)
	}
	return js.sc.scanned
}

// go-reviewer nit 3: with a linear scan the pending cap is 1 MiB again, so
// large but legitimate objects decode; too deep objects are counted too.
func TestJSONLargeObjectsDecodeAndDropsAreCounted(t *testing.T) {
	big := []byte(`{"path":"scene/name","value":"` + strings.Repeat("y", 200<<10) + `"}`)
	deep := []byte(strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2))
	s := JSON{}.NewStream(netpkt.TCP, nil)
	got := feedAll(s, [][]byte{[]byte("{"), deep, []byte("}"), big})
	if len(got) != 1 || !strings.HasPrefix(got[0].key, "scene/name") {
		t.Fatalf("a 200 KiB object did not decode: %d messages", len(got))
	}
	if n := s.(Dropper).Dropped(); n != 1 {
		t.Fatalf("Dropped() = %d, want 1 (the too deep object)", n)
	}
}
