// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// Redesign CC-4 (AC-RD-S02 core part, SR-27 amended): the 20 s idle relock
// of a Gain unlock never runs out while a Gain hold on that headamp is
// open (a finger resting on the wheel), and the 20 s start again at
// release.
func TestShowMode_IdleRelockPausesWhileGainIsHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		if err := sup.Unlock(gain3, gesture); err != nil {
			t.Fatal(err)
		}
		if err := sup.Hold(gain3, gesture, true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Second) // the finger rests on the wheel
		synctest.Wait()
		r, err := sup.Write(set(gain3, lvl(t, 34.5), gesture)) // then moves 96 pt: +2 dB
		if err != nil || r.State != StateFree || lastWrite(drv) != 34.5 {
			t.Fatalf("after a 25 s rest: %v %s %v", dbIn(t, r), r.State, err)
		}
		if err := sup.Hold(gain3, gesture, false); err != nil {
			t.Fatal(err)
		}
		time.Sleep(19 * time.Second)
		synctest.Wait()
		if u := sup.Drain(); !slices.Equal(u.Unlocked, []int{3}) {
			t.Fatalf("19 s after release: unlocked %v, ended %+v", u.Unlocked, u.UnlockEnded)
		}
		time.Sleep(time.Second + time.Millisecond)
		synctest.Wait()
		if u := sup.Drain(); len(u.Unlocked) != 0 || !slices.Equal(u.UnlockEnded, []UnlockEvent{{Headamp: 3, Reason: UnlockIdle}}) {
			t.Fatalf("20 s after release: %+v %+v", u.Unlocked, u.UnlockEnded)
		}
	})
}
