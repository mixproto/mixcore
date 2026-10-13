// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Hashes from docs/protocols/wing.md section 3.6 (WG-F32).
const (
	hashCh1Fader  = 0x38AE75C2
	hashCh1Mute   = 0xF50F69F8
	hashCh1Name   = 0xC6AD548D
	hashLibActive = 0xDF6A06E2
)

// TestEncode_DocExamples pins the byte strings of wing.md section 3.6.
func TestEncode_DocExamples(t *testing.T) {
	name, err := AppendString(AppendHash(nil, hashCh1Name), "Kick")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		what string
		got  []byte
		want string
	}{
		{"select the data channel", AppendSelect(nil, ChannelData), "DF D1"},
		{"read ch 1 fader", AppendValueRead(nil, hashCh1Fader), "D7 38AE75C2 DC"},
		{"set ch 1 fader to -10 dB", AppendFloat(AppendHash(nil, hashCh1Fader), -10), "D7 38AE75C2 D5 C1200000"},
		{"set ch 1 mute on (1 = muted)", AppendBool(AppendHash(nil, hashCh1Mute), true), "D7 F50F69F8 01"},
		{"set ch 1 mute off", AppendBool(AppendHash(nil, hashCh1Mute), false), "D7 F50F69F8 00"},
		{"definitions of the root's children", AppendDefRequest(AppendRoot(nil)), "DA DD"},
		{"read /$ctl/lib/$active, DF escaped", AppendFrame(nil, ChannelData, AppendValueRead(nil, hashLibActive)), "DF D1 D7 DF DE 6A 06 E2 DC"},
		{"set ch 1 name to Kick", name, "D7 C6AD548D 83 4B 69 63 6B"},
		{"1790.5 Hz, DF D0 inside a float escaped (WG-F103)", AppendFrame(nil, ChannelData, AppendFloat(nil, 1790.5)), "DF D1 D5 44 DF DE D0 00"},
	}
	for _, tc := range tests {
		if want := mustHex(t, tc.want); !bytes.Equal(tc.got, want) {
			t.Errorf("%s: got % X, want % X", tc.what, tc.got, want)
		}
	}
}

func TestDemux_DocExampleAndRules(t *testing.T) {
	tests := []struct {
		what, in, want string
	}{
		{"WG-F13 vendor example", "D702DFDEAF0E02", "D702DFAF0E02"},
		{"bytes before any selection are data", "0102", "0102"},
		{"another channel is dropped", "DFD3 0102 DFD1 03", "03"},
		{"DF and another byte is a literal DF and that byte", "DF41", "DF41"},
		{"DF DF is two literal bytes", "DFDF", "DFDF"},
	}
	for _, tc := range tests {
		var d Demux
		if got := d.Append(nil, mustHex(t, tc.in), ChannelData); !bytes.Equal(got, mustHex(t, tc.want)) {
			t.Errorf("%s: got % X, want %s", tc.what, got, tc.want)
		}
	}
}

func TestDemux_EscapeSplitAcrossChunks(t *testing.T) {
	var d Demux
	out := d.Append(nil, []byte{0x01, Escape}, ChannelData)
	out = d.Append(out, []byte{escLiteral, 0x02}, ChannelData)
	if want := []byte{0x01, Escape, 0x02}; !bytes.Equal(out, want) {
		t.Fatalf("got % X, want % X", out, want)
	}
}

func TestParse_DocPushAndTokens(t *testing.T) {
	b := mustHex(t, "D7 38AE75C2 D5 C3100000") // vendor /*b capture: ch 1 fader at -oo (WG-F60)
	tok, n, err := Parse(b)
	if err != nil || tok.Kind != KindHash || tok.Hash != hashCh1Fader || n != 5 {
		t.Fatalf("hash token: %+v %d %v", tok, n, err)
	}
	tok, n, err = Parse(b[n:])
	if err != nil || tok.Kind != KindFloat || tok.Float != -144 || n != 5 {
		t.Fatalf("float token: %+v %d %v", tok, n, err)
	}
	cases := []struct {
		in   string
		want Token
		n    int
	}{
		{"00", Token{Kind: KindInt, Int: 0}, 1},
		{"3F", Token{Kind: KindInt, Int: 63}, 1},
		{"40", Token{Kind: KindNodeIndex, Int: 1}, 1},
		{"7F", Token{Kind: KindNodeIndex, Int: 64}, 1},
		{"D2 0100", Token{Kind: KindNodeIndex, Int: 257}, 3},
		{"D3 FFFE", Token{Kind: KindInt, Int: -2}, 3},
		{"D4 00010000", Token{Kind: KindInt, Int: 65536}, 5},
		{"D9 FF", Token{Kind: KindStep, Int: -1}, 2},
		{"DE", Token{Kind: KindEnd}, 1},
		{"DC", Token{Kind: KindDataRequest}, 1},
	}
	for _, tc := range cases {
		got, n, err := Parse(mustHex(t, tc.in))
		if err != nil || n != tc.n || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%s) = %+v, %d, %v", tc.in, got, n, err)
		}
	}
}

func TestParse_Strings(t *testing.T) {
	for _, s := range []string{"", "K", strings.Repeat("a", 64), strings.Repeat("b", 65), strings.Repeat("c", 256)} {
		b, err := AppendString(nil, s)
		if err != nil {
			t.Fatal(err)
		}
		tok, n, err := Parse(b)
		if err != nil || tok.Kind != KindString || string(tok.Bytes) != s || n != len(b) {
			t.Fatalf("string of %d: %+v %d %v", len(s), tok.Kind, n, err)
		}
	}
	if _, err := AppendString(nil, strings.Repeat("x", 257)); !errors.Is(err, ErrValue) {
		t.Fatalf("257 bytes: %v", err)
	}
}

func TestParse_ShortInputWaits(t *testing.T) {
	for _, in := range []string{"D7 38AE", "D5 C3", "85 41", "D1", "D1 05 41", "DF 00", "DF 0000 0000", "DF 0004 0000"} {
		if _, _, err := Parse(mustHex(t, in)); !errors.Is(err, ErrShort) {
			t.Errorf("Parse(%s): %v, want ErrShort", in, err)
		}
	}
}

func TestParse_OversizedDefinitionRejected(t *testing.T) {
	if _, _, err := Parse(mustHex(t, "DF 0000 00100000")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("1 MiB definition: %v", err)
	}
}

func TestAppendInt_Ranges(t *testing.T) {
	for _, v := range []int32{0, 1, 63, 64, -1, math.MaxInt16, math.MinInt16, math.MaxInt16 + 1, math.MinInt32} {
		tok, n, err := Parse(AppendInt(nil, v))
		if err != nil || tok.Kind != KindInt || tok.Int != v {
			t.Fatalf("int %d: %+v %d %v", v, tok, n, err)
		}
	}
}

func TestDef_RoundTripEveryType(t *testing.T) {
	defs := []Def{
		{Parent: 0x70B10139, Hash: hashCh1Fader, Index: 5, Name: "fdr", LongName: "Fader", Type: TypeFader, Unit: UnitDB},
		{Parent: 1, Hash: 2, Index: 1, Name: "g", LongName: "Gain", Type: TypeLinFloat, Unit: UnitDB, Min: -2.5, Max: 45, Steps: 20},
		{Parent: 1, Hash: 3, Index: 2, Name: "f", Type: TypeLogFloat, Unit: UnitHz, Min: 20, Max: 20000, Steps: 961},
		{Parent: 1, Hash: 4, Index: 3, Name: "mute", Type: TypeInt, IntMin: 0, IntMax: 1},
		{Parent: 1, Hash: 5, Index: 4, Name: "grp", Type: TypeStringEnum, Items: []EnumItem{{Name: "OFF", Long: "Off"}, {Name: "LCL", Long: "Local"}}},
		{Parent: 1, Hash: 6, Index: 5, Name: "ratio", Type: TypeFloatEnum, Items: []EnumItem{{Value: 1.1, Long: "1.1"}, {Value: 100, Long: "100"}}},
		{Parent: 1, Hash: 7, Index: 6, Name: "$name", Type: TypeString, ReadOnly: true, MaxLen: 16},
		{Parent: 0, Hash: 0x70B10139, Index: 1, Name: "ch", LongName: "Channels", Type: TypeNode},
	}
	for _, d := range defs {
		body, err := AppendDefBody(nil, d)
		if err != nil {
			t.Fatal(err)
		}
		tok, n, err := Parse(AppendDefToken(nil, body))
		if err != nil || tok.Kind != KindDef || n != 3+len(body) {
			t.Fatalf("%s token: %v", d.Name, err)
		}
		got, err := ParseDef(tok.Bytes)
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		if !reflect.DeepEqual(d, got) {
			t.Fatalf("%s round trip: got %+v, want %+v", d.Name, got, d)
		}
	}
}

func TestParseDef_RejectsTruncatedAndHugeCounts(t *testing.T) {
	body, _ := AppendDefBody(nil, Def{Name: "grp", Type: TypeStringEnum, Items: []EnumItem{{Name: "OFF"}}})
	for cut := range len(body) {
		if _, err := ParseDef(body[:cut]); !errors.Is(err, ErrBadDef) {
			t.Fatalf("cut at %d: %v", cut, err)
		}
	}
	huge := append(body[:len(body)-7:len(body)-7], 0xFF, 0xFF) // count 65535, no items
	if _, err := ParseDef(huge); !errors.Is(err, ErrBadDef) {
		t.Fatalf("huge count: %v", err)
	}
}

// TestMeter_VendorExamples pins WG-F51 and WG-F52.
func TestMeter_VendorExamples(t *testing.T) {
	req, err := AppendMeterRequest(nil, 0x3737, 2, []MeterBlock{{Type: MeterChannel, Indexes: []int{2}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, "D33737 D400000002 DCA001DE"); !bytes.Equal(req, want) {
		t.Fatalf("request % X, want % X", req, want)
	}
	if got, want := AppendFrame(nil, ChannelMeters, AppendMeterRenew(nil, 2)), mustHex(t, "DFD3 D400000002"); !bytes.Equal(got, want) {
		t.Fatalf("renew % X", got)
	}
	pkt := mustHex(t, "00000002 f9fb f9fb fcfb fcfb f9fb 0000 ee01 0000")
	if id, ok := MeterPacketID(pkt); !ok || id != 2 {
		t.Fatalf("id %d %v", id, ok)
	}
	for i, want := range []float64{-6, -6, -3, -3, -6, 0, -18, 0} {
		w, ok := MeterWord(pkt, i)
		if !ok || math.Abs(float64(MeterDBFS(w))-want) > 0.05 {
			t.Fatalf("word %d = %v dB, want about %v", i, MeterDBFS(w), want)
		}
	}
	if _, ok := MeterWord(pkt, 8); ok {
		t.Fatal("word past the end")
	}
	if MeterDBFS(math.MinInt16) != float32(math.Inf(-1)) || math.Abs(float64(MeterDBFS(-0x644E))+100.3) > 0.01 {
		t.Fatal("8000 must be -inf and 9bb2 about -100.3 dB")
	}
	if _, err := AppendMeterRequest(nil, 1, 1, []MeterBlock{{Type: MeterChannel, Indexes: []int{129}}}); !errors.Is(err, ErrMeterRequest) {
		t.Fatalf("index 129: %v", err)
	}
}

// TestParse_UndefinedBytesRejected: E0..FF are not tokens (WG-F30).
func TestParse_UndefinedBytesRejected(t *testing.T) {
	for b := 0xE0; b <= 0xFF; b++ {
		if _, _, err := Parse([]byte{byte(b), 0, 0, 0, 0}); !errors.Is(err, ErrUnknownToken) {
			t.Fatalf("%#x: %v", b, err)
		}
	}
}
