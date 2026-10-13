// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/meters"
)

func hexBytes(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// feedAll runs bytes through the demux and the assembler like the reader.
func feedAll(in []byte) ([]inMsg, error) {
	var (
		mux  wingproto.Demux
		asm  assembler
		msgs []inMsg
	)
	data := mux.Append(nil, in, wingproto.ChannelData)
	_, err := asm.feed(data, time.Time{}, func(m inMsg) bool { msgs = append(msgs, m); return true })
	return msgs, err
}

func TestAssembler_PushAndSplitTokens(t *testing.T) {
	msgs, err := feedAll(hexBytes(t, "DFD1 D7 38AE75C2 D5 C3100000 D7 DFDE6A06E2 80 41 DE DA 05"))
	if err != nil || len(msgs) != 3 {
		t.Fatalf("%v %+v", err, msgs)
	}
	if msgs[0].hash != 0x38AE75C2 || msgs[0].val.f != -144 || msgs[1].hash != 0xDF6A06E2 || msgs[1].val.s != "A" || msgs[2].kind != msgEnd {
		t.Fatalf("messages %+v", msgs)
	}
	// A token split across reads waits for its rest.
	var asm assembler
	var got []inMsg
	rest, _ := asm.feed(hexBytes(t, "D7 38AE75C2 D5 C3"), time.Time{}, func(m inMsg) bool { got = append(got, m); return true })
	rest = append(rest, hexBytes(t, "100000")...)
	_, _ = asm.feed(rest, time.Time{}, func(m inMsg) bool { got = append(got, m); return true })
	if len(got) != 1 || got[0].val.f != -144 {
		t.Fatalf("split token: %+v", got)
	}
}

// FuzzAssembler: malformed streams never panic; every value message has a hash.
func FuzzAssembler(f *testing.F) {
	f.Add(hexBytes(f, "DFD1 D7 38AE75C2 D5 C3100000"))
	f.Add(hexBytes(f, "DFDE 0010 00000000 70B10139 0001 0263680000 00"))
	f.Add(hexBytes(f, "D7 D7 D7 41 DE DC DD"))
	f.Fuzz(func(t *testing.T, in []byte) {
		msgs, _ := feedAll(in)
		for _, m := range msgs {
			if m.kind == 0 {
				t.Fatal("empty message")
			}
		}
	})
}

// meterPacket builds a 708-byte packet: every channel input at chDB, main
// 1 output at mainDB (WG-F52, WG-F53).
func meterPacket(id uint32, chDB, mainDB float64) []byte {
	pkt := make([]byte, meterPacketLen)
	pkt[0], pkt[1], pkt[2], pkt[3] = byte(id>>24), byte(id>>16), byte(id>>8), byte(id)
	put := func(word int, db float64) {
		v := int16(math.Round(db * 256))
		pkt[4+2*word], pkt[5+2*word] = byte(uint16(v)>>8), byte(v)
	}
	for ch := range meterChannels {
		put(ch*8, chDB)
		put(ch*8+1, chDB)
	}
	put(meterMainBlock*8+2, mainDB)
	put(meterMainBlock*8+3, mainDB)
	return pkt
}

func TestDecodeMeterPacket(t *testing.T) {
	var fr meters.Frame
	decodeMeterPacket(meterPacket(1, -6, -3), &fr, 38)
	if fr.Count != 42 || fr.Levels[0] != -6 || fr.Levels[37] != -6 || !math.IsInf(float64(fr.Levels[38]), -1) || fr.Levels[40] != -3 || fr.Levels[41] != -3 {
		t.Fatalf("frame %v", fr.Levels[:42])
	}
}

func TestMeterSink_DropsForeignPackets(t *testing.T) {
	slot := &meters.Slot{}
	m := &meterSink{slot: slot}
	m.switchOn(true, 7, 40)
	m.handle(meterPacket(8, -6, -3), time.Now())       // another report id
	m.handle(meterPacket(7, -6, -3)[:700], time.Now()) // wrong length
	var fr meters.Frame
	if slot.CopyLatest(&fr) {
		t.Fatal("published a foreign packet")
	}
	m.handle(meterPacket(7, -6, -3), time.Now())
	if !slot.CopyLatest(&fr) || fr.Levels[0] != -6 {
		t.Fatal("own packet not published")
	}
}

// TestAssembler_UndefinedTokenDropsStream: an undefined byte ends the
// connection (stale, reconnect, full re-read) instead of a guess that could
// misread payload bytes as a parameter change.
func TestAssembler_UndefinedTokenDropsStream(t *testing.T) {
	msgs, err := feedAll(hexBytes(t, "D7 38AE75C2 D5 C1200000 E5 D7 38AE75C2 D5 41200000"))
	if !errors.Is(err, errStream) || len(msgs) != 1 {
		t.Fatalf("%v %+v", err, msgs)
	}
}

// TestAssembler_DefinitionsOutsideAWalkAreSkipped (review H2).
func TestAssembler_DefinitionsOutsideAWalkAreSkipped(t *testing.T) {
	body, _ := wingproto.AppendDefBody(nil, wingproto.Def{Hash: 1, Name: "x", Type: wingproto.TypeInt})
	stream := append(wingproto.AppendDefToken(nil, body), hexBytes(t, "D7 38AE75C2 D5 C1200000")...)
	asm := assembler{skipDefs: func() bool { return true }}
	var got []inMsg
	_, _ = asm.feed(stream, time.Time{}, func(m inMsg) bool { got = append(got, m); return true })
	if len(got) != 1 || got[0].kind != msgValue {
		t.Fatalf("messages %+v", got)
	}
}

// TestSceneBurst_BoundedUnderFlood (review sec M3): a flood of pushes of a
// few parameters within one burst window keeps the window's bookkeeping
// bounded (no growth with the number of pushes) and raises no recall.
func TestSceneBurst_BoundedUnderFlood(t *testing.T) {
	s := &session{}
	s.own.scene = newSceneState()
	at := time.Unix(1000, 0)
	for i := range 5000 {
		s.onConsolePush(i%3, at)
	}
	if n := s.own.scene.burst.Len(); n > 2*3+16 { // bounded by the distinct parameters, not the pushes
		t.Fatalf("window holds %d entries for 3 parameters", n)
	}
	if s.own.scene.open {
		t.Fatal("3 parameters are no burst")
	}
	for i := range 8 {
		s.onConsolePush(100+i, at)
	}
	if !s.own.scene.open {
		t.Fatal("8 distinct parameters within 250 ms are a burst")
	}
}
