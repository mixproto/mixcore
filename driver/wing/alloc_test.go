// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package wing

import (
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/meters"
)

// TestHotPaths_ZeroAlloc enforces the allocation budget of the hot paths
// (go-core-standards section 9): a fader push, a fader write and a meter packet.
func TestHotPaths_ZeroAlloc(t *testing.T) {
	push := hexBytes(t, "D7 38AE75C2 D5 C1200000")
	var asm assembler
	deliver := func(inMsg) bool { return true }
	if n := testing.AllocsPerRun(100, func() { _, _ = asm.feed(push, time.Time{}, deliver) }); n != 0 {
		t.Errorf("fader push decode: %v allocs", n)
	}
	buf := make([]byte, 0, 64)
	if n := testing.AllocsPerRun(100, func() {
		f, _ := faderToWire(-10)
		buf = wingproto.AppendFloat(wingproto.AppendHash(buf[:0], 0x38AE75C2), f)
	}); n != 0 {
		t.Errorf("fader write encode: %v allocs", n)
	}
	pkt := meterPacket(1, -6, -3)
	var fr meters.Frame
	if n := testing.AllocsPerRun(100, func() { decodeMeterPacket(pkt, &fr, meterChannels) }); n != 0 {
		t.Errorf("meter decode: %v allocs", n)
	}
}

func BenchmarkDecodeFaderPush(b *testing.B) {
	push := hexBytes(b, "D7 38AE75C2 D5 C1200000")
	var asm assembler
	deliver := func(inMsg) bool { return true }
	b.ReportAllocs()
	for b.Loop() {
		_, _ = asm.feed(push, time.Time{}, deliver)
	}
}

func BenchmarkDecodeMeterPacket(b *testing.B) {
	pkt := meterPacket(1, -6, -3)
	var fr meters.Frame
	b.ReportAllocs()
	for b.Loop() {
		decodeMeterPacket(pkt, &fr, meterChannels)
	}
}

func BenchmarkEncodeFaderWrite(b *testing.B) {
	tokens, wire := make([]byte, 0, 64), make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		f, _ := faderToWire(-10)
		tokens = wingproto.AppendFloat(wingproto.AppendHash(tokens[:0], 0x38AE75C2), f)
		wire = wingproto.AppendEscaped(wire[:0], tokens)
	}
}
