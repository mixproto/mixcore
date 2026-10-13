// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/testkit"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// driver.Session's non-blocking contract (review Sec L4): with Run not
// started, so nothing drains the session, thousands of writes, meter and
// re-read requests and held-set changes all return at once. The
// supervisor calls them under its lock.
func TestSession_CallsNeverBlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		sess := r.connect()
		held := []model.ParamRef{ref(1, model.FieldFader)}
		err := testkit.ReturnsWithin(time.Second, func() {
			for i := range 5000 {
				v, _ := model.Level(-float64(i % 60))
				_ = sess.Apply(driver.Command{Param: ref(1+i%12, model.FieldFader), Value: v, Origin: gesture})
				_ = sess.SetMeters(driver.MeterRequest{Inputs: i%2 == 0})
				_ = sess.(driver.SelectiveRereader).RereadExcept(held)
				sess.(driver.HeldSetter).SetHeld(held)
			}
		})
		if err != nil {
			t.Fatalf("a Session call blocked: %v", err)
		}
	})
}
