// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package osc

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// Golden vectors printed in docs/protocols/x32.md (OSC section). Each one must decode to
// the listed message and re-encode to exactly the same bytes.
var goldenVectors = []struct {
	name string
	hex  string
	addr string
	args []Arg
}{
	{
		name: "fader set ch01 0.8250",
		hex:  "2f63682f30312f6d69782f6661646572000000002c6600003f5334cd",
		addr: "/ch/01/mix/fader",
		args: []Arg{Float(math.Float32frombits(0x3f5334cd))}, // fader step 844 of 1023
	},
	{
		name: "pan set ch02 0.75",
		hex:  "2f63682f30322f6d69782f70616e00002c6600003f400000",
		addr: "/ch/02/mix/pan",
		args: []Arg{Float(0.75)},
	},
	{
		name: "eq q float",
		hex:  "2f63682f30312f65712f312f710000002c6600003eedfa44",
		addr: "/ch/01/eq/1/q",
		args: []Arg{Float(0.4648)},
	},
	{
		name: "enum as string",
		hex:  "2f63682f30312f676174652f6d6f6465000000002c7300004741544500000000",
		addr: "/ch/01/gate/mode",
		args: []Arg{String("GATE")},
	},
	{
		name: "enum as int",
		hex:  "2f63682f30312f676174652f6d6f6465000000002c69000000000003",
		addr: "/ch/01/gate/mode",
		args: []Arg{Int(3)},
	},
	{
		name: "meters request string and int",
		hex:  "2f6d6574657273002c7369002f6d65746572732f3600000000000010",
		addr: "/meters",
		args: []Arg{String("/meters/6"), Int(16)},
	},
	{
		name: "meters reply with blob",
		hex:  "2f6d65746572732f360000002c6200000000001404000000fd1d2137fdff7f3f0000803f6ebbd534",
		addr: "/meters/6",
		args: []Arg{Blob(mustHexStatic("04000000fd1d2137fdff7f3f0000803f6ebbd534"))},
	},
}

func mustHexStatic(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDecode_GoldenVectors(t *testing.T) {
	for _, tc := range goldenVectors {
		t.Run(tc.name, func(t *testing.T) {
			var m Message
			if err := Decode(mustHex(t, tc.hex), &m); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			want := Message{Address: []byte(tc.addr)}
			for _, a := range tc.args {
				want.appendArg(a)
			}
			if !m.Equal(&want) {
				t.Fatalf("decoded %s, want %s", m.String(), want.String())
			}
		})
	}
}

func TestAppend_GoldenVectors(t *testing.T) {
	for _, tc := range goldenVectors {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AppendString(nil, tc.addr, tc.args...)
			if err != nil {
				t.Fatalf("AppendString: %v", err)
			}
			if want := mustHex(t, tc.hex); !bytes.Equal(got, want) {
				t.Fatalf("encoded\n %x\nwant\n %x", got, want)
			}
		})
	}
}

func TestDecode_UntaggedForms(t *testing.T) {
	tests := []struct {
		name        string
		in          []byte
		wantArgs    int
		wantRawLen  int
		wantTagless bool
	}{
		{"address only, no type tag (older OSC form)", []byte("/info\x00\x00\x00"), 0, 0, true},
		{"empty type tag", []byte("/info\x00\x00\x00,\x00\x00\x00"), 0, 0, false},
		{"untagged payload kept raw", []byte("/x\x00\x00\x00\x00\x00\x01"), 0, 4, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m Message
			if err := Decode(tc.in, &m); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if m.NumArgs() != tc.wantArgs || len(m.Untagged) != tc.wantRawLen || m.Tagless != tc.wantTagless {
				t.Fatalf("got args=%d raw=%d tagless=%v", m.NumArgs(), len(m.Untagged), m.Tagless)
			}
			out, err := Append(nil, &m)
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			if !bytes.Equal(out, tc.in) {
				t.Fatalf("round trip %x, want %x", out, tc.in)
			}
		})
	}
}

func TestDecode_Rejects(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"empty", nil, ErrTruncated},
		{"no leading slash", []byte("abc\x00"), ErrBadAddress},
		{"bundle", []byte("#bundle\x00"), ErrBundle},
		{"address not terminated", []byte("/abc"), ErrTruncated},
		{"address not padded", []byte("/ab\x00\x00"), ErrTruncated},
		{"unknown type tag", []byte("/a\x00\x00,x\x00\x00"), ErrUnsupportedType},
		{"int truncated", []byte("/a\x00\x00,i\x00\x00\x00\x00"), ErrTruncated},
		{"blob size too large", []byte("/a\x00\x00,b\x00\x00\x00\x00\x00\x09abcd"), ErrTruncated},
		{"blob size negative", []byte("/a\x00\x00,b\x00\x00\xff\xff\xff\xff"), ErrTruncated},
		{"string not terminated", []byte("/a\x00\x00,s\x00\x00abcd"), ErrTruncated},
		{"trailing bytes after args", []byte("/a\x00\x00,\x00\x00\x00\x00\x00\x00\x01"), ErrTrailingData},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m Message
			if err := Decode(tc.in, &m); !errors.Is(err, tc.want) {
				t.Fatalf("Decode(%x) error = %v, want %v", tc.in, err, tc.want)
			}
		})
	}
}

func TestDecode_TooManyArgs(t *testing.T) {
	tags := make([]Arg, MaxArgs+1)
	for i := range tags {
		tags[i] = Int(int32(i))
	}
	b, err := AppendString(nil, "/a", tags...)
	if !errors.Is(err, ErrTooManyArgs) {
		t.Fatalf("AppendString with %d args: err = %v, want ErrTooManyArgs", len(tags), err)
	}
	if b != nil {
		t.Fatalf("expected nil output on error, got %x", b)
	}
}

func TestAppend_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		addr string
		args []Arg
		want error
	}{
		{"empty address", "", nil, ErrBadAddress},
		{"no slash", "ch/01", nil, ErrBadAddress},
		{"NUL in address", "/a\x00b", nil, ErrBadAddress},
		{"NUL in string", "/a", []Arg{String("x\x00y")}, ErrBadString},
		{"unknown type", "/a", []Arg{{Type: 'x'}}, ErrUnsupportedType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AppendString(nil, tc.addr, tc.args...); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAppend_ReusesBuffer(t *testing.T) {
	buf := make([]byte, 0, 64)
	out, err := AppendString(buf[:0], "/ch/01/mix/fader", Float(0.75))
	if err != nil {
		t.Fatal(err)
	}
	if &out[0] != &buf[:1][0] {
		t.Fatal("AppendString did not reuse the provided buffer")
	}
}

func TestMessage_Accessors(t *testing.T) {
	b, err := AppendString(nil, "/xinfo", String("10.0.0.2"), String("X32-01"), String("X32"), String("4.06"))
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	if err := Decode(b, &m); err != nil {
		t.Fatal(err)
	}
	if !m.AddressIs("/xinfo") || m.AddressIs("/xinf") {
		t.Fatalf("AddressIs mismatch for %q", m.Address)
	}
	if s, ok := m.StringArg(1); !ok || s != "X32-01" {
		t.Fatalf("StringArg(1) = %q, %v", s, ok)
	}
	if _, ok := m.FloatArg(0); ok {
		t.Fatal("FloatArg on a string argument must fail")
	}
	if _, ok := m.IntArg(9); ok {
		t.Fatal("IntArg out of range must fail")
	}
	if got := m.String(); got != `/xinfo ,ssss "10.0.0.2" "X32-01" "X32" "4.06"` {
		t.Fatalf("String() = %s", got)
	}
}
