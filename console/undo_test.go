// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Redesign CC-6 (core-api v0.5 rule 14, SR-13 and SR-29 amended, AC-RD-U01,
// U02, U05, U06): the undo history lives in the core, per client.

// newestUndo returns the client's newest undo summary of an update.
// echoOf delivers the console's echo of a write (the last session).
func echoOf(t *testing.T, sup *Supervisor, p model.ParamRef, v float64, o driver.Origin) {
	t.Helper()
	sup.mu.Lock()
	sess := sup.sess
	sup.mu.Unlock()
	fs, ok := sess.(*fakeSession)
	if !ok {
		t.Fatal("not a fake session")
	}
	fs.push <- driver.ParamChanged{Param: p, Value: lvl(t, v), Origin: o}
	synctest.Wait()
}

func newestUndo(t *testing.T, u Update, client string) UndoSummary {
	t.Helper()
	i := slices.IndexFunc(u.Undo, func(s UndoSummary) bool { return s.ClientID == client })
	if i < 0 {
		t.Fatalf("no undo entry for %s in %+v", client, u.Undo)
	}
	return u.Undo[i]
}

// holdMove grabs p, writes values (each echoed, as x32 and xair echo a
// fader) and lets go.
func holdMove(t *testing.T, sup *Supervisor, p model.ParamRef, o driver.Origin, values ...float64) {
	t.Helper()
	if err := sup.Hold(p, o, true); err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if _, err := sup.Write(set(p, lvl(t, v), o)); err != nil {
			t.Fatal(err)
		}
		echoOf(t, sup, p, v, o)
	}
	if err := sup.Hold(p, o, false); err != nil {
		t.Fatal(err)
	}
}

// AC-RD-U01: a hold is one entry from its grab value to its last write; the
// state names it with its direction; one undo writes the grab value once
// and the entry leaves the history.
func TestUndo_HoldIsOneEntryAndUndoWritesTheStartOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		holdMove(t, sup, ch1Fader, gesture, -4, -2)
		e := newestUndo(t, sup.Drain(), gesture.ClientID)
		if e.Param != ch1Fader || e.Kind != UndoLevel || dbOf(e.From) != -6 || dbOf(e.To) != -2 || e.Delta != 4 ||
			!e.HasDelta || e.Direction != DirectionUp || len(e.Asks) != 0 {
			t.Fatalf("entry %+v", e)
		}
		time.Sleep(30 * time.Second) // any age: no age question (SR-13 amended)
		n := len(drv.session(0).writes())
		r, err := sup.Undo(gesture, false)
		if err != nil || !r.Written || lastWrite(drv) != -6 || len(drv.session(0).writes()) != n+1 {
			t.Fatalf("undo: %+v %v, last write %v", r, err, lastWrite(drv))
		}
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNothingToUndo) || !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("second undo: %v", err)
		}
	})
}

// AC-RD-U02: an undo that unmutes asks and writes nothing until confirmed.
func TestUndo_UnmuteAsks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		mute := model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldMute}
		if err := sup.Toggle(mute, gesture); err != nil {
			t.Fatal(err)
		}
		e := newestUndo(t, sup.Drain(), gesture.ClientID)
		if e.Kind != UndoSwitch || e.Direction != DirectionNone || !slices.Equal(e.Asks, []string{AskUnmute}) {
			t.Fatalf("entry %+v", e)
		}
		n := len(drv.session(0).writes())
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNeedsConfirm) || len(drv.session(0).writes()) != n {
			t.Fatalf("unconfirmed unmute: %v", err)
		}
		if _, err := sup.Undo(gesture, true); err != nil {
			t.Fatal(err)
		}
		if w := drv.session(0).writes(); len(w) != n+1 || !w[n].Value.Equal(model.Bool(false)) {
			t.Fatalf("writes %+v", w)
		}
	})
}

// AC-RD-U05: an undo that raises a level this device lowered less than
// 30 s ago asks; the same entry 40 s old undoes in one tap.
func TestUndo_RaiseAfterRecentLowerAsks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-2))
		holdMove(t, sup, ch1Fader, gesture, -3.5, -5) // the feedback pull-down
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); !slices.Equal(e.Asks, []string{AskRaiseAfterRecentLower}) || e.Direction != DirectionDown {
			t.Fatalf("20 s: %+v", e)
		}
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNeedsConfirm) {
			t.Fatalf("undo 20 s after the pull-down: %v", err)
		}
		time.Sleep(20 * time.Second) // 40 s: the core tells the UI the question is gone
		synctest.Wait()
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); len(e.Asks) != 0 {
			t.Fatalf("40 s: %+v", e)
		}
		if _, err := sup.Undo(gesture, false); err != nil || lastWrite(drv) != -2 {
			t.Fatalf("undo 40 s after: %v, %v", err, lastWrite(drv))
		}
	})
}

// SR-13: raises over 6 dB, conflicts and Gain moves over 6 dB ask.
func TestUndo_BigRaiseConflictAndGainAsk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.Write(Write{Command: driver.Command{Param: ch1Fader, Value: lvl(t, -20), Origin: gesture}, Typed: true}); err != nil {
			t.Fatal(err)
		}
		echoOf(t, sup, ch1Fader, -20, gesture)
		time.Sleep(31 * time.Second)
		synctest.Wait()
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); !slices.Equal(e.Asks, []string{AskRaiseOver6dB}) {
			t.Fatalf("raise of 14 dB: %+v", e)
		}
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -10), Origin: desk}
		synctest.Wait()
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); !slices.Equal(e.Asks, []string{AskConflict}) {
			t.Fatalf("after the desk moved it: %+v", e)
		}
		holdMove(t, sup, gain3, gesture, 36.5, 39.5)
		time.Sleep(31 * time.Second)
		synctest.Wait()
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); !slices.Equal(e.Asks, []string{AskGainOver6dB}) {
			t.Fatalf("Gain undo of 7 dB: %+v", e)
		}
		if _, err := sup.Undo(gesture, true); err != nil || lastWrite(drv) != 32.5 {
			t.Fatalf("confirmed Gain undo: %v %v", err, lastWrite(drv))
		}
	})
}

// AC-RD-U06, CC-10: the history is cut at a scene recall at the console:
// undoing across it asks, naming the recall.
func TestUndo_SceneRecallCutsTheHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		holdMove(t, sup, ch1Fader, gesture, -4)
		time.Sleep(31 * time.Second)
		drv.session(0).push <- driver.SceneRecalled{Confidence: driver.ConfidenceLow, LoadedScene: 4, SceneName: "Song 4"}
		synctest.Wait()
		e := newestUndo(t, sup.Drain(), gesture.ClientID)
		if !slices.Contains(e.Asks, AskBeforeSceneRecall) || e.Scene == nil || *e.Scene != (SceneMark{Number: 4, Name: "Song 4"}) {
			t.Fatalf("entry after the recall: %+v %+v", e, e.Scene)
		}
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNeedsConfirm) {
			t.Fatalf("undo across the recall: %v", err)
		}
	})
}

// Fader-and-gain 5.2: stepper taps within 1 s merge into one entry.
func TestUndo_TapsWithinOneSecondMerge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		for _, v := range []float64{33.5, 34.5, 35.5} {
			if _, err := sup.Write(set(gain3, lvl(t, v), gesture)); err != nil {
				t.Fatal(err)
			}
			pushGain(t, drv, v, gesture, false)
			time.Sleep(400 * time.Millisecond)
		}
		u := sup.Drain()
		e := newestUndo(t, u, gesture.ClientID)
		if dbOf(e.From) != 32.5 || dbOf(e.To) != 35.5 {
			t.Fatalf("merged entry %+v", e)
		}
		if _, err := sup.Undo(gesture, false); err != nil || lastWrite(drv) != 32.5 {
			t.Fatalf("undo of the taps: %v %v", err, lastWrite(drv))
		}
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNothingToUndo) {
			t.Fatalf("one entry only: %v", err)
		}
	})
}

// Undo of a parameter the client still holds is refused: cancelHold is the
// way (rule 14).
func TestUndo_HeldParameterIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		holdMove(t, sup, ch1Fader, gesture, -4)
		_ = sup.Hold(ch1Fader, gesture, true)
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrUndoHeld) {
			t.Fatalf("undo while held: %v", err)
		}
	})
}

// Every client has its own history (a hub's devices).
func TestUndo_PerClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		holdMove(t, sup, ch1Fader, engineerB, -4)
		if _, err := sup.Undo(gesture, false); !errors.Is(err, ErrNothingToUndo) {
			t.Fatalf("another client's entry: %v", err)
		}
		if e := newestUndo(t, sup.Drain(), engineerB.ClientID); dbOf(e.To) != -4 {
			t.Fatalf("%+v", e)
		}
	})
}

// Redesign CC-9 (AC-RD-U04): cancelHold writes the grab value once, ends
// the hold (the finger must lift and grab again) and makes no entry.
func TestCancelHold_WritesTheGrabValueOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(ch1Fader, gesture, true)
		for _, v := range []float64{-4, -1} {
			_, _ = sup.Write(set(ch1Fader, lvl(t, v), gesture))
		}
		n := len(drv.session(0).writes())
		r, err := sup.CancelHold(ch1Fader, gesture)
		if err != nil || !r.Written || dbIn(t, r) != -6 || len(drv.session(0).writes()) != n+1 || lastWrite(drv) != -6 {
			t.Fatalf("cancel: %+v %v, writes %d", r, err, len(drv.session(0).writes())-n)
		}
		if _, err := sup.Write(set(ch1Fader, lvl(t, 0), gesture)); !errors.Is(err, ErrGestureEnded) {
			t.Fatalf("the held finger moves on: %v", err)
		}
		_ = sup.Hold(ch1Fader, gesture, false)
		if u := sup.Drain(); slices.ContainsFunc(u.Undo, func(s UndoSummary) bool { return s.ClientID == gesture.ClientID }) {
			t.Fatalf("cancelHold made an entry: %+v", u.Undo)
		}
		if _, err := sup.CancelHold(ch1Fader, gesture); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("cancel without a hold: %v", err)
		}
	})
}

// A hold that wrote nothing is cancelled without a write.
func TestCancelHold_NothingWrittenNothingToRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(ch1Fader, gesture, true)
		if r, err := sup.CancelHold(ch1Fader, gesture); err != nil || r.Written || len(drv.session(0).writes()) != 0 {
			t.Fatalf("%+v %v", r, err)
		}
	})
}

// A hold ended by the desk is an entry that already meets a conflict: the
// console is not where the drag left it.
func TestUndo_HoldEndedByTheDeskIsAConflict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Hold(ch1Fader, gesture, true)
		_, _ = sup.Write(set(ch1Fader, lvl(t, -4), gesture))
		echoOf(t, sup, ch1Fader, -4, gesture)
		drv.session(0).push <- driver.ParamChanged{Param: ch1Fader, Value: lvl(t, -12), Origin: desk}
		synctest.Wait()
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); !slices.Equal(e.Asks, []string{AskConflict}) || dbOf(e.To) != -4 {
			t.Fatalf("entry %+v", e)
		}
	})
}

// A new entry reaches the dispatcher even when no echo follows: one
// coalesced signal at most undoSignalDelay after a burst of writes.
func TestUndo_EntrySignalsOnceForABurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.Drain()
		select {
		case <-sup.Notify():
		default:
		}
		for _, v := range []float64{-5, -4, -3} { // no echo: the fake console says nothing
			if _, err := sup.Write(set(ch1Fader, lvl(t, v), gesture)); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-sup.Notify():
			t.Fatal("signalled at once: one signal per write would wake the dispatcher per Apply")
		default:
		}
		time.Sleep(undoSignalDelay)
		synctest.Wait()
		select {
		case <-sup.Notify():
		default:
			t.Fatal("no signal after the delay")
		}
		if e := newestUndo(t, sup.Drain(), gesture.ClientID); dbOf(e.To) != -3 {
			t.Fatalf("entry %+v", e)
		}
	})
}

// Review item 8: a scene name from a driver reaches the state clean.
func TestScene_NameIsCleanedInTheCore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		holdMove(t, sup, ch1Fader, gesture, -4)
		drv.session(0).push <- driver.SceneRecalled{Confidence: driver.ConfidenceLow, LoadedScene: 2, SceneName: "\x1b[2JSong\u202e 2\n"}
		synctest.Wait()
		u := sup.Drain()
		if len(u.Scenes) != 1 || u.Scenes[0].SceneName != "[2JSong 2" {
			t.Fatalf("scenes %+v", u.Scenes)
		}
		if e := newestUndo(t, u, gesture.ClientID); e.Scene == nil || e.Scene.Name != "[2JSong 2" {
			t.Fatalf("entry %+v", e)
		}
	})
}
