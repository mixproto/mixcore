// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package meters

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestSlot_LatestWins(t *testing.T) {
	var s Slot
	var dst Frame
	if s.CopyLatest(&dst) {
		t.Fatal("empty slot must report nothing new")
	}
	for seq := uint64(1); seq <= 3; seq++ {
		f := Frame{Seq: seq, Count: 1}
		f.Levels[0] = float32(seq)
		s.Publish(&f)
	}
	if !s.CopyLatest(&dst) || dst.Seq != 3 || dst.Levels[0] != 3 {
		t.Fatalf("got seq %d level %v, want the newest frame 3", dst.Seq, dst.Levels[0])
	}
	if s.CopyLatest(&dst) {
		t.Fatal("second copy of the same frame must report nothing new")
	}
	s.Reset()
	if !s.CopyLatest(&dst) || dst.Count != 0 {
		t.Fatalf("after Reset the reader must get one empty frame, got seq %d count %d", dst.Seq, dst.Count)
	}
}

func TestLinearToDBFS(t *testing.T) {
	tests := []struct {
		in   float32
		want float64
	}{
		{1, 0},
		{0.5, -6.0206},
		{8, 18.0618},
		{0, math.Inf(-1)},
		{-1, math.Inf(-1)},
		{float32(math.NaN()), math.Inf(-1)},
		{float32(math.Inf(1)), math.Inf(-1)},
	}
	for _, tc := range tests {
		got := float64(LinearToDBFS(tc.in))
		if math.IsInf(tc.want, -1) {
			if !math.IsInf(got, -1) {
				t.Fatalf("LinearToDBFS(%v) = %v, want -inf", tc.in, got)
			}
			continue
		}
		if math.Abs(got-tc.want) > 1e-3 {
			t.Fatalf("LinearToDBFS(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAppendBinary_Layout(t *testing.T) {
	at := time.Unix(1, 500)
	f := Frame{Seq: 7, At: at, Count: 2}
	f.Levels[0] = -12
	f.Levels[1] = float32(math.Inf(-1))
	b := AppendBinary(nil, &f)
	if len(b) != FrameHeaderSize+2*levelSize {
		t.Fatalf("len = %d", len(b))
	}
	if string(b[:4]) != FrameMagic || binary.LittleEndian.Uint16(b[4:]) != FrameVersion ||
		binary.LittleEndian.Uint16(b[6:]) != 2 || binary.LittleEndian.Uint64(b[8:]) != 7 ||
		int64(binary.LittleEndian.Uint64(b[16:])) != at.UnixNano() {
		t.Fatalf("bad header %x", b[:FrameHeaderSize])
	}
	if l := math.Float32frombits(binary.LittleEndian.Uint32(b[24:])); l != -12 {
		t.Fatalf("level 0 = %v", l)
	}
	if l := math.Float32frombits(binary.LittleEndian.Uint32(b[28:])); !math.IsInf(float64(l), -1) {
		t.Fatalf("level 1 = %v, want -inf", l)
	}
}
