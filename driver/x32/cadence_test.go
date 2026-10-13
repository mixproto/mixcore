// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// coreRereadBound is the recovery bound of x32.md "Background re-read" for
// a v0.3 parameter (fader, mute, Gain, groups) with buses and sends
// present: every pass re-reads them all, then one slice of the rest.
const coreRereadBound = time.Minute

// rereadEvery mimics the supervisor: a pass is asked for every 15 s.
func rereadEvery(t *testing.T, rr driver.Rereader, total time.Duration) {
	t.Helper()
	for start := time.Now(); time.Since(start) < total; time.Sleep(15 * time.Second) {
		if err := rr.Reread(); err != nil {
			t.Fatal(err)
		}
	}
}

// Review Go S3 and lead decision (OQ 67): faders, mutes and Gain first.
// With every send of the x32 present, a channel fader is re-read at least
// once a minute, and a send still comes round within ten minutes.
func TestReread_CoreParamsWithinTheBoundWithSendsPresent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		r.run(sess)
		rereadEvery(t, sess.(driver.Rereader), 10*time.Minute)
		at := reads(r.sim, "/ch/01/mix/fader")[1:] // [0]: connect
		if len(at) < 2 {
			t.Fatalf("ch 1 fader re-read %d times in 10 min", len(at))
		}
		for i := 1; i < len(at); i++ {
			if gap := at[i].At.Sub(at[i-1].At); gap > coreRereadBound {
				t.Fatalf("ch 1 fader re-read %v after the previous, bound %v", gap, coreRereadBound)
			}
		}
		for _, addr := range []string{"/ch/32/mix/16/level", "/ch/01/mix/01/type", "/mtx/06/mix/on"} {
			if n := len(reads(r.sim, addr)); n < 2 {
				t.Fatalf("%s re-read %d times in 10 min, want at least once after connect", addr, n-1)
			}
		}
	})
}

// Review Go S3: a hold that begins in the middle of a pass is left out of
// that same pass (SetHeld, per batch), not only from the next one.
func TestReread_HoldBegunMidPassIsSkippedAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		r.run(sess)
		hs, ok := sess.(driver.HeldSetter)
		if !ok {
			t.Fatal("the x32 session must implement driver.HeldSetter")
		}
		held := model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldColour} // first of the slice
		before := len(reads(r.sim, "/ch/01/config/color"))
		if err := sess.(driver.Rereader).Reread(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second) // the pass is in its v0.3 part
		hs.SetHeld([]model.ParamRef{held})
		time.Sleep(rereadPassTime)
		if n := len(reads(r.sim, "/ch/01/config/color")); n != before {
			t.Fatalf("a parameter held mid-pass was re-read %d times", n-before)
		}
	})
}
