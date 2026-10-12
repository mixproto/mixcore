// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"errors"
	"math"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func testDriver() *Driver { return New(Profile{Family: "test", Layout: x32Layout}, Timing{}) }

func inputRef(i int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: i}, Field: f}
}

func TestRereadReply_Bookkeeping(t *testing.T) {
	s := newSession(testDriver(), nil, driver.Options{})
	s.known = make([]model.Value, len(s.params.all))
	s.inputs = x32Layout.Inputs
	idx := s.params.index(inputRef(2, model.FieldFader))
	s.known[idx] = mustLevel(t, -20)
	now := time.Now()

	tests := []struct {
		name    string
		asked   bool
		touched bool
		value   model.Value
		want    bool
	}{
		{"late reply of an earlier step is dropped", false, false, mustLevel(t, -5), false},
		{"reply crossed by a write or push is dropped", true, true, mustLevel(t, -5), false},
		{"reply equal to the known value is dropped", true, false, mustLevel(t, -20), false},
		{"reply with a new value is a console change", true, false, mustLevel(t, -5), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clear(s.rr.asked)
			if tc.asked {
				s.rr.asked[idx] = false
				if tc.touched {
					s.markTouched(idx)
				}
			}
			ev, ok := s.rereadReply(idx, tc.value, now)
			if ok != tc.want {
				t.Fatalf("reported = %v, want %v", ok, tc.want)
			}
			if ok && (ev.Origin.Kind != driver.OriginConsole || !s.known[idx].Equal(tc.value)) {
				t.Fatalf("event %+v, known %v", ev, s.known[idx])
			}
		})
	}
}

func TestReread_ClosedSession(t *testing.T) {
	s := newSession(testDriver(), nil, driver.Options{})
	s.stopped.Store(true)
	if err := s.Reread(); !errors.Is(err, driver.ErrClosed) {
		t.Fatalf("Reread on a closed session = %v", err)
	}
}

func FuzzParseXinfo(f *testing.F) {
	ok, _ := osc.AppendString(nil, "/xinfo", osc.String("192.168.1.62"), osc.String("X32-02-4A-53"),
		osc.String("X32"), osc.String("3.04")) // X32-F10 example
	f.Add(ok)
	air, _ := osc.AppendString(nil, "/xinfo", osc.String("192.168.1.1"), osc.String("XR18-1D-DA-B4"),
		osc.String("XR18"), osc.String("1.12")) // XA-F10 shape with the XS4 /info values
	f.Add(air)
	f.Add([]byte("/xinfo\x00\x00,s\x00\x00a\x00\x00\x00"))
	f.Add([]byte("/xinfo\x00\x00,\x00\x00\x00")) // our own broadcast seen by the probe socket (XA-F13)
	f.Fuzz(func(t *testing.T, b []byte) {
		var m osc.Message
		if osc.Decode(b, &m) != nil {
			return
		}
		_, _ = ParseXinfo(&m) // must not panic
	})
}

func TestParseXinfo_OwnBroadcastIsRejected(t *testing.T) {
	var m osc.Message
	pkt, _ := osc.AppendString(nil, "/xinfo")
	if err := osc.Decode(pkt, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseXinfo(&m); !errors.Is(err, errBadIdentity) {
		t.Fatalf("a request without 4 strings must be rejected (XA-F13): %v", err)
	}
}

func TestDBToFader_NearestStepAtSegmentBreak(t *testing.T) {
	// X32-F41: the PDF table puts -30 dB at step 256; int(f*1023.5) would give 255.
	f, _ := dbToFader(-30)
	if k := math.Round(float64(f) * faderSteps); k != 256 {
		t.Fatalf("-30 dB is step %v, want 256", k)
	}
}

func BenchmarkEncodeFaderWrite(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		f, err := dbToFader(-12.5)
		if err != nil {
			b.Fatal(err)
		}
		if buf, err = osc.AppendString(buf[:0], "/ch/01/mix/fader", osc.Float(f)); err != nil {
			b.Fatal(err)
		}
	}
}
