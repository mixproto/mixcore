// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// The core-api v0.5 commands through the API against sim/xair (rules 14
// to 17): every state message stays valid against state.schema.json.

const (
	vox1Mute = `{"strip":{"kind":"input","index":1},"field":"mute"}`
	send42   = `{"strip":{"kind":"input","index":4},"field":"sendLevel","bus":2}`
)

func assertMessagesValid(t *testing.T, e *xairEnv) {
	t.Helper()
	schemas := loadSchemas(t)
	e.rec.mu.Lock()
	defer e.rec.mu.Unlock()
	for _, m := range e.rec.raw {
		if errs := schemas.validate("state.schema.json", m); len(errs) != 0 {
			t.Fatalf("%s\nviolates state.schema.json: %v", m, errs)
		}
	}
}

func joinedState(e *xairEnv) string {
	e.rec.mu.Lock()
	defer e.rec.mu.Unlock()
	return strings.Join(e.rec.raw, "\n")
}

func settle(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

// AC-RD-U01, U02, U04 through the API.
func TestRedesignAPI_UndoAndCancelHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{}`)
		gestureResult(t, e, cmdJSON("holdBegin", vox1, ""))
		for _, v := range []string{"-4", "-2"} {
			gestureResult(t, e, cmdJSON("set", vox1, `,"value":`+v))
		}
		gestureResult(t, e, cmdJSON("holdEnd", vox1, ""))
		settle(100 * time.Millisecond)
		want := `"undo":[{"clientId":"ui","param":{"strip":{"kind":"input","index":1},"field":"fader"},"kind":"level","from":-5.99`
		if !strings.Contains(joinedState(e), want) || !strings.Contains(joinedState(e), `"direction":"up","asks":[]}]`) {
			t.Fatalf("no undo entry %s in\n%s", want, joinedState(e))
		}
		if r := gestureResult(t, e, cmdJSON("undo", "", "")); !r.Written {
			t.Fatalf("undo: %+v", r)
		}
		settle(100 * time.Millisecond)
		if f, _ := e.sim.Value("/ch/01/mix/fader"); e.eng.FaderDB(float64(f.Float)) > -5.9 {
			t.Fatalf("mixer Vox1 %v after undo", e.eng.FaderDB(float64(f.Float)))
		}
		if err := e.eng.Apply(cmdJSON("undo", "", "")); CodeOf(err) != CodeUnsupported {
			t.Fatalf("nothing to undo: %v", err)
		}
		if err := e.eng.Apply(cmdJSON("toggle", vox1Mute, "")); err != nil {
			t.Fatal(err)
		}
		settle(100 * time.Millisecond)
		if err := e.eng.Apply(cmdJSON("undo", "", "")); CodeOf(err) != CodeNeedsConfirm {
			t.Fatalf("undo of a mute: %v", err)
		}
		if err := e.eng.Apply(cmdJSON("undo", "", `,"confirmed":true`)); err != nil {
			t.Fatal(err)
		}
		gestureResult(t, e, cmdJSON("holdBegin", vox1, ""))
		gestureResult(t, e, cmdJSON("set", vox1, `,"value":0`))
		if r := gestureResult(t, e, cmdJSON("cancelHold", vox1, "")); !r.Written {
			t.Fatalf("cancelHold: %+v", r)
		}
		if err := e.eng.Apply(cmdJSON("set", vox1, `,"value":3`)); CodeOf(err) != CodeGestureEnded {
			t.Fatalf("the finger moves on after cancelHold: %v", err)
		}
		settle(100 * time.Millisecond)
		assertMessagesValid(t, e)
	})
}

// AC-RD-X02 core part: another client changes the bus this client shows.
func TestRedesignAPI_WatchReportsAnotherClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{}`)
		if err := e.eng.Apply(cmdJSON("watch", "", `,"scope":{"bus":2}`)); err != nil {
			t.Fatal(err)
		}
		other := strings.Replace(cmdJSON("set", send42, `,"value":-12`), `"clientId":"ui"`, `"clientId":"keys-iem"`, 1)
		if err := e.eng.Apply(other); err != nil {
			t.Fatal(err)
		}
		settle(200 * time.Millisecond)
		want := `{"type":"externalChangeInScope","clientId":"ui","scope":{"bus":2},"count":1}`
		if !strings.Contains(joinedState(e), want) {
			t.Fatalf("no %s in\n%s", want, joinedState(e))
		}
		if err := e.eng.Apply(cmdJSON("watch", "", `,"scope":{"bus":0}`)); CodeOf(err) != CodeInvalidCommand {
			t.Fatalf("bus 0: %v", err)
		}
		assertMessagesValid(t, e)
	})
}

// AC-RD-S04 and AC-RD-P08 through the API.
func TestRedesignAPI_ShowModePINAndFavourites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newXAirEnv(t, simxair.Options{})
		startWith(t, e, `{}`)
		out, err := e.eng.Gesture(cmdJSON("lockShowMode", "", `,"pin":"2468"`))
		var r gestureResultJSON
		if err != nil || json.Unmarshal([]byte(out), &r) != nil || !strings.HasPrefix(r.PinRecord, "pbkdf2-sha256$") {
			t.Fatalf("lockShowMode: %s %v", out, err)
		}
		for cmd, code := range map[string]string{
			cmdJSON("setShowMode", "", `,"value":false`):                                 CodeRole,
			cmdJSON("unlock", gain3, ""):                                                 CodeRole,
			cmdJSON("unlockShowMode", "", `,"pin":"1111"`):                               CodeRole,
			cmdJSON("unlockShowMode", "", `,"pin":"24680"`):                              CodeInvalidCommand,
			cmdJSON("set", gain3, `,"value":31.5`):                                       "",
			cmdJSON("setFavourites", "", `,"favourites":[{"kind":"headamp","index":3}]`): CodeInvalidCommand,
		} {
			if err := e.eng.Apply(cmd); CodeOf(err) != code {
				t.Fatalf("%s: %v, want %q", cmd, err, code)
			}
		}
		settle(100 * time.Millisecond)
		if !strings.Contains(joinedState(e), `"showModeLocked":true`) || strings.Contains(joinedState(e), r.PinRecord) {
			t.Fatal("the state says locked and never carries the record")
		}
		if err := e.eng.Apply(cmdJSON("unlockShowMode", "", `,"pin":"2468"`)); err != nil {
			t.Fatal(err)
		}
		if err := e.eng.Apply(cmdJSON("setFavourites", "", `,"favourites":[{"kind":"bus","index":2},{"kind":"input","index":1}]`)); err != nil {
			t.Fatal(err)
		}
		settle(100 * time.Millisecond)
		if !strings.Contains(joinedState(e), `"favourites":[{"clientId":"ui","strips":[{"kind":"bus","index":2},{"kind":"input","index":1}]}]`) {
			t.Fatal("no favourites in the state")
		}
		if n := len(e.sim.Writes()); n != 1 { // only the Gain -1
			t.Fatalf("%d writes", n)
		}
		assertMessagesValid(t, e)
		restartWithLock(t, e, r.PinRecord)
	})
}

// restartWithLock starts again with the stored record and the favourites:
// show mode is locked and the layer is back.
func restartWithLock(t *testing.T, e *xairEnv, record string) {
	t.Helper()
	_ = e.eng.Stop()
	cfg := `{"version":0,"family":"xair","host":"10.0.0.1","safety":{"showModeLock":{"pinRecord":"` + record +
		`"}},"favourites":[{"kind":"main"}]}`
	if _, err := e.eng.Start(cfg); err != nil {
		t.Fatal(err)
	}
	settle(time.Second)
	st := e.rec.last()
	if !st.ShowMode || !st.ShowModeLocked || st.Favourites == nil || len(*st.Favourites) != 1 || len((*st.Favourites)[0].Strips) != 1 {
		t.Fatalf("after restart: showMode %v locked %v favourites %v", st.ShowMode, st.ShowModeLocked, st.Favourites)
	}
	_ = e.eng.Stop()
	if _, err := e.eng.Start(strings.Replace(cfg, record, "pbkdf2-sha256$5$a$b", 1)); CodeOf(err) != CodeInvalidConfig {
		t.Fatalf("a broken record: %v", err)
	}
	_ = e.sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.6))
}
