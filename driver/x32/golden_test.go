// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

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
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/wire_trace.golden")

// TestWire_GoldenTrace pins every byte the x32 driver puts on the wire: the
// ordered connect sequence and the distinct packets of a live session with
// writes, meters, a background re-read and a scene recall. Any refactor of
// the driver must leave this file unchanged (go test -run Golden -update
// rewrites it, which a review must then justify).
func TestWire_GoldenTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.sim.SetInputLevels([]float32{1, 0.5})
		wt := &testkit.WireTrace{}
		opts := r.opts()
		opts.ListenPacket = wt.Listen(opts.ListenPacket)
		sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, opts)
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
		{Param: ref(1, model.FieldFader), Value: mustLevel(t, -10), Origin: g},
		{Param: ref(2, model.FieldMute), Value: model.Bool(true), Origin: g},
		{Param: ref(3, model.FieldName), Value: name, Origin: g},
		{Param: grp(model.StripMuteGroup, 2), Value: model.Bool(true), Origin: g},
		{Param: grp(model.StripDCA, 1), Value: model.Bool(true), Origin: g},
		{Param: model.ParamRef{Strip: model.MainRef, Field: model.FieldFader}, Value: mustLevel(t, 0), Origin: g},
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
	time.Sleep(14 * time.Second)
	if err := r.sim.RecallScene(1, faderScene(10)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	_ = sess.SetMeters(driver.MeterRequest{})
	synctest.Wait()
}
