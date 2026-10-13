// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/testkit"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// driver.Session's non-blocking contract (review Sec L4): with Run not
// started, so nothing drains the session, thousands of writes, meter and
// re-read requests and held-set changes all return at once. The
// supervisor calls them under its lock.
func TestSession_CallsNeverBlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		sess := r.connect()
		held := []model.ParamRef{ch1Fader}
		err := testkit.ReturnsWithin(time.Second, func() {
			for i := range 5000 {
				v, _ := model.Level(-float64(i % 60))
				_ = sess.Apply(driver.Command{Param: ref(model.StripInput, 1+i%12, model.FieldFader), Value: v, Origin: gesture})
				_ = sess.SetMeters(driver.MeterRequest{Inputs: i%2 == 0})
				_ = driver.SelectiveRereader(sess).RereadExcept(held)
				driver.HeldSetter(sess).SetHeld(held)
			}
		})
		if err != nil {
			t.Fatalf("a Session call blocked: %v", err)
		}
	})
}

// TestCapabilities_CoreAPIV04: WING reports its echo window and fader grid
// (WG-F60) and no buses, matrices, sends, DCA masters or colours yet, so
// none of them is faked.
func TestCapabilities_CoreAPIV04(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		s := r.connect()
		c := s.Capabilities()
		if c.EchoWindow != DefaultTiming.EchoWindow || c.FaderSteps != faderSteps || c.SendLevelSteps != 0 ||
			len(c.Buses) != 0 || c.Matrices != 0 || c.TapLabels != ([model.TapCount]string{}) {
			t.Fatalf("capabilities %+v", c)
		}
		st := s.Snapshot()
		if st.Shape() != (model.Shape{}) || len(st.Buses()) != 0 {
			t.Fatalf("shape %+v, %d buses", st.Shape(), len(st.Buses()))
		}
	})
}

// TestSetHeld_LeavesHeldOutOfEveryPass: a held parameter is not re-read,
// in this pass or the next (driver.HeldSetter).
func TestSetHeld_LeavesHeldOutOfEveryPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		s := r.connect()
		r.run(s)
		s.SetHeld([]model.ParamRef{ch1Fader})
		time.Sleep(time.Second) // past the connect-time reads
		start := time.Now()
		for range 2 {
			_ = s.Reread()
			time.Sleep(DefaultTiming.RereadGap + time.Second)
		}
		synctest.Wait()
		others := 0
		for _, e := range r.sim.Log() {
			recent := e.Class == simwing.ClassRead && !e.At.Before(start)
			if recent && e.Path == "/ch/1/fdr" {
				t.Fatalf("a held fader was re-read at %v", e.At)
			}
			if recent && e.Path == "/ch/2/fdr" {
				others++
			}
		}
		if others < 2 {
			t.Fatalf("an unheld fader was re-read %d times in two passes", others)
		}
	})
}
