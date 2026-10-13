// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
)

// Review re-check blocker (lead decision): when the values in flight span
// more than 12 dB, no Gain value is within 6 dB of all of them and of the
// console. A hold's write then writes nothing (Written false, the hold
// stays open, the start is reported) until the settle read-back brings the
// values back together; it never writes the edge of an empty range.

// dragUnechoed holds gain3 for o and writes 33.5 .. 52.5 in 1 dB steps on a
// console that does not echo Gain.
func dragUnechoed(t *testing.T, sup *Supervisor, o driver.Origin) {
	t.Helper()
	_ = sup.Hold(gain3, o, true)
	for db := 33.5; db <= 52.5; db++ {
		if _, err := sup.Write(set(gain3, lvl(t, db), o)); err != nil {
			t.Fatal(err)
		}
	}
}

// The two-engineer probe (zz_probe6): A drags 33.5 to 52.5, B grabs (A's
// hold ends), sees 52.5 and nudges -0.5.
func TestGain_EmptyBoundWritesNothingTwoEngineers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		dragUnechoed(t, sup, gesture)
		r, err := sup.HoldWindow(gain3, engineerB, true)
		if err != nil || dbIn(t, r) != 52.5 {
			t.Fatalf("B grabs: %+v %v", r, err)
		}
		n := len(drv.session(0).writes())
		r, err = sup.Write(set(gain3, lvl(t, 52), engineerB))
		if err != nil || r.Written || len(drv.session(0).writes()) != n || dbIn(t, r) != 52.5 {
			t.Fatalf("B's nudge wrote %v (written %t, %v), want nothing, start reported", dbIn(t, r), r.Written, err)
		}
		pushGain(t, drv, 52.5, desk, true) // the settle read-back
		if r, err := sup.Write(set(gain3, lvl(t, 52), engineerB)); err != nil || !r.Written || lastWrite(drv) != 52 {
			t.Fatalf("after the read-back: %+v %v", r, err)
		}
	})
}

// The lost-values probe: the same client's next hold, before the read-back.
func TestGain_EmptyBoundWritesNothingNextHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		dragUnechoed(t, sup, gesture)
		_ = sup.Hold(gain3, gesture, false)
		_ = sup.Hold(gain3, gesture, true)
		n := len(drv.session(0).writes())
		if r, err := sup.Write(set(gain3, lvl(t, 51.5), gesture)); err != nil || r.Written || len(drv.session(0).writes()) != n {
			t.Fatalf("first write of the next hold: %v written %t %v", dbIn(t, r), r.Written, err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 51), gesture)); err != nil {
			t.Fatalf("the hold stays open: %v", err)
		}
	})
}
