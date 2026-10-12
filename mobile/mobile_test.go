// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mobile

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestCallsBeforeStartReturnStableCodes(t *testing.T) {
	tests := []struct {
		name string
		call func() error
		want string
	}{
		{"Stop", Stop, ErrNotStarted},
		{"Pause", Pause, ErrNotStarted},
		{"Resume", Resume, ErrNotStarted},
		{"RequestSnapshot", RequestSnapshot, ErrNotStarted},
		{"Apply bad JSON", func() error { return Apply("{") }, ErrInvalidCommand},
		{"Start bad JSON", func() error { _, err := Start(`{"version":0}`); return err }, ErrInvalidConfig},
		{"Discover bad JSON", func() error { return Discover(`{}`) }, ErrInvalidConfig},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || err.Error() != tc.want {
				t.Fatalf("%s = %v, want the bare code %s", tc.name, err, tc.want)
			}
		})
	}
}

func TestFaderLawAndEmptyMeterFrame(t *testing.T) {
	if FaderPosition(0) != 0.75 || !math.IsInf(FaderDB(0), -1) || FaderDB(0.5) != -10 {
		t.Fatal("fader law of the x32 family expected before Start")
	}
	b := MeterFrame()
	if len(b) != 24 || string(b[:4]) != "MXMF" || binary.LittleEndian.Uint16(b[6:]) != 0 {
		t.Fatalf("frame before start = %x", b)
	}
	if MeterFrameMaxBytes != 24+4*128 {
		t.Fatalf("MeterFrameMaxBytes = %d", MeterFrameMaxBytes)
	}
}

func TestNilCallbacksAreAccepted(t *testing.T) {
	SetStateListener(nil)
	SetDiscoveryListener(nil)
	SetLogSink(nil, LogInfo)
	SetSocketBinder(nil)
}

func TestRecoverErrTurnsPanicsIntoErrInternal(t *testing.T) {
	boom := func() (err error) {
		defer recoverErr(&err)
		panic("bug")
	}
	if err := boom(); !errors.Is(err, errInternal) || err.Error() != ErrInternal {
		t.Fatalf("panic became %v", err)
	}
	quiet := func() (x float64) {
		defer recoverQuiet()
		panic("bug")
	}
	if quiet() != 0 {
		t.Fatal("recoverQuiet must return the zero value")
	}
}

// ReadMeterFrame fills the caller's buffer and never allocates per frame.
func TestReadMeterFrame_CallerBuffer(t *testing.T) {
	buf := make([]byte, MeterFrameMaxBytes)
	n := ReadMeterFrame(buf)
	if n != 24 || string(buf[:4]) != "MXMF" {
		t.Fatalf("ReadMeterFrame = %d bytes %q, want the empty 24-byte frame", n, buf[:4])
	}
	if got := ReadMeterFrame(make([]byte, 3)); got != n {
		t.Fatalf("short buffer: %d, want the needed size %d", got, n)
	}
	if allocs := testing.AllocsPerRun(100, func() { ReadMeterFrame(buf) }); allocs != 0 {
		t.Fatalf("ReadMeterFrame allocates %v times per call", allocs)
	}
}
