// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"context"
	"flag"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/testkit"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/wire_trace.golden")

// TestWire_GoldenTrace pins every byte the xair driver puts on the wire: the
// ordered connect sequence and the distinct packets of a live session with
// writes, meters, a background re-read and a snapshot load. A review checks
// each address against docs/protocols/xair.md section 3 and each request
// against XA-F20 and XA-F57 (go test -run Golden -update rewrites it).
func TestWire_GoldenTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simxair.Options{PushSnapIndex: true})
		r.sim.SetMeterValues([]int16{0, -4608})
		if err := r.sim.StoreSnapshot(3, "Band", burstScene(10)); err != nil {
			t.Fatal(err)
		}
		wt := &testkit.WireTrace{}
		opts := r.opts()
		opts.ListenPacket = wt.Listen(opts.ListenPacket)
		sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: mixerIP}, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		connect := wt.Take()
		r.run(sess)
		goldenSession(t, r, sess)
		live := wt.Take()
		got := testkit.Render(connect, live, []string{"subscription", "command"})
		testkit.Golden(t, filepath.Join("testdata", "wire_trace.golden"), got, *updateGolden)
	})
}

func goldenSession(t *testing.T, r *rig, sess driver.Session) {
	t.Helper()
	g := driver.Origin{Kind: driver.OriginGesture, ClientID: "golden", Seq: 1}
	name, _ := model.Text("Vox")
	for _, c := range []driver.Command{
		{Param: ref(1, model.FieldFader), Value: level(t, -10), Origin: g},
		{Param: ref(2, model.FieldMute), Value: model.Bool(true), Origin: g},
		{Param: ref(3, model.FieldName), Value: name, Origin: g},
		{Param: grp(model.StripMuteGroup, 2), Value: model.Bool(true), Origin: g},
		{Param: grp(model.StripDCA, 1), Value: model.Bool(true), Origin: g},
		{Param: mainRef(model.FieldFader), Value: level(t, 0), Origin: g},
		{Param: mainRef(model.FieldMute), Value: model.Bool(true), Origin: g},
	} {
		if err := sess.Apply(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if err := sess.(driver.Rereader).Reread(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(8 * time.Second)
	if err := r.sim.LoadSnapshot(3); err != nil {
		t.Fatal(err)
	}
	time.Sleep(8 * time.Second)
	_ = sess.SetMeters(driver.MeterRequest{})
	synctest.Wait()
}
