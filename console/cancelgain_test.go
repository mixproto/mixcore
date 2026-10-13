// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/model"
)

// Review nit 7 (lead decision): cancelHold that lowers Gain back to the
// grab value is always allowed, however far the drag went (in a feedback
// emergency the safe direction is never refused); one that would raise
// Gain by more than 6 dB stays refused and the hold stays open.
func TestCancelHold_GainLowersAnyAmountRaisesAtMostSix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(gain3, gesture, true)
		for db := 33.5; db <= 44.5; db++ {
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
			pushGain(t, drv, db, gesture, false)
		}
		r, err := sup.CancelHold(gain3, gesture)
		if err != nil || !r.Written || lastWrite(drv) != 32.5 {
			t.Fatalf("cancel 12 dB down to the grab value: %+v %v, last write %v", r, err, lastWrite(drv))
		}
		pushGain(t, drv, 32.5, gesture, false)
		_ = sup.Hold(gain3, gesture, false)
		_ = sup.Hold(gain3, gesture, true)
		for db := 31.5; db >= 20.5; db-- {
			_, _ = sup.Write(set(gain3, lvl(t, db), gesture))
			pushGain(t, drv, db, gesture, false)
		}
		n := len(drv.session(0).writes())
		if _, err := sup.CancelHold(gain3, gesture); !errors.Is(err, ErrGainBound) || len(drv.session(0).writes()) != n {
			t.Fatalf("cancel 12 dB up: %v", err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 21.5), gesture)); err != nil {
			t.Fatalf("the refused cancel keeps the hold open: %v", err)
		}
	})
}

// Review nit 7: the question an undo asks is not a typed raise.
func TestUndo_ConfirmErrorNamesTheUndo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		mute := model.ParamRef{Strip: ch1Fader.Strip, Field: model.FieldMute}
		if err := sup.Toggle(mute, gesture); err != nil {
			t.Fatal(err)
		}
		_, err := sup.Undo(gesture, false)
		if !errors.Is(err, ErrNeedsConfirm) || strings.Contains(err.Error(), "typed") || !strings.Contains(err.Error(), "undo") {
			t.Fatalf("error %q", err)
		}
	})
}
