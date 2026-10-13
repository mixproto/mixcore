// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

var ch1Fader = ref(model.StripInput, 1, model.FieldFader)

// findChange returns the last change of p.
func findChange(cs []driver.ParamChanged, p model.ParamRef) (driver.ParamChanged, bool) {
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Param == p {
			return cs[i], true
		}
	}
	return driver.ParamChanged{}, false
}

// TestEcho_BothHypotheses (WG-F24, wing.md 2.4): without an echo the
// read-back settles the write as our own (OwnWrite, never ending a hold);
// with an echo the push carries the write's origin.
func TestEcho_BothHypotheses(t *testing.T) {
	for _, echo := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, simwing.Options{EchoToWriter: echo})
			rn := r.run(r.connect())
			if err := rn.s.Apply(driver.Command{Param: ch1Fader, Value: level(t, -10), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			cs := rn.changes(time.Second)
			if v, _ := r.sim.Value("/ch/1/fdr"); v != -10.0 {
				t.Fatalf("console fader %v", v)
			}
			for _, c := range cs {
				external := c.Param == ch1Fader && c.Origin.Kind == driver.OriginConsole && !c.OwnWrite
				if external {
					t.Fatalf("echo %v: our own write reported as an external change: %+v", echo, c)
				}
			}
			last, ok := findChange(cs, ch1Fader)
			if !ok || !last.Authoritative || !last.OwnWrite {
				t.Fatalf("echo %v: the read-back must settle the write as ours: %+v", echo, cs)
			}
			if echo && cs[0].Origin != gesture {
				t.Fatalf("the echo push must carry the gesture origin: %+v", cs[0])
			}
			if a := r.audits(); len(a) != 1 || a[0].Address != "/ch/1/fdr" || a[0].Origin != gesture {
				t.Fatalf("write audit %+v", a)
			}
		})
	}
}

// TestEcho_ConsoleRoundsDB: a console that rounds dB writes to 0.1 dB
// (item 10) still settles our write as ours.
func TestEcho_ConsoleRoundsDB(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{RoundFaderDB: 0.1, EchoToWriter: true})
		rn := r.run(r.connect())
		_ = rn.s.Apply(driver.Command{Param: ch1Fader, Value: level(t, -3.33), Origin: gesture})
		for _, c := range rn.changes(time.Second) {
			if c.Param == ch1Fader && c.Origin.Kind == driver.OriginConsole && !c.OwnWrite {
				t.Fatalf("rounded value taken as an external change: %+v", c)
			}
		}
	})
}

// TestHeldFader_ExternalChangeIsConsoleOrigin: a desk move is a console
// change (it ends a held gesture in the supervisor).
func TestHeldFader_ExternalChangeIsConsoleOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		_ = rn.s.Apply(driver.Command{Param: ch1Fader, Value: level(t, -10), Origin: gesture})
		time.Sleep(50 * time.Millisecond)
		mustSet(t, r.sim, "/ch/1/fdr", -20.0)
		c, ok := findChange(rn.changes(time.Second), ch1Fader)
		db, _ := c.Value.DB()
		if !ok || db != -20 || c.Origin.Kind != driver.OriginConsole || c.OwnWrite {
			t.Fatalf("desk move: %+v", c)
		}
	})
}

// TestApply_Refusals: rule 2 (no write without a gesture), read-only
// parameters, the TCP remote lock (WG-F26) and invalid values.
func TestApply_Refusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		s := rn.s
		cases := []struct {
			cmd  driver.Command
			want error
		}{
			{driver.Command{Param: ch1Fader, Value: level(t, 0), Origin: driver.Origin{Kind: driver.OriginSystem}}, driver.ErrNotUserOrigin},
			{driver.Command{Param: ch1Fader, Value: level(t, 0), Origin: driver.Origin{Kind: driver.OriginConsole}}, driver.ErrNotUserOrigin},
			{driver.Command{Param: ref(model.StripInput, 1, model.FieldDCAs), Value: model.Mask(1), Origin: gesture}, driver.ErrUnsupported},
			{driver.Command{Param: ref(model.StripHeadamp, 1, model.FieldPhantom), Value: model.Bool(true), Origin: gesture}, driver.ErrUnsupported},
			{driver.Command{Param: ref(model.StripHeadamp, 9, model.FieldGain), Value: level(t, 10), Origin: gesture}, driver.ErrUnsupported},
			{driver.Command{Param: ref(model.StripHeadamp, 101, model.FieldGain), Value: level(t, 10), Origin: gesture}, driver.ErrUnsupported},
			{driver.Command{Param: ref(model.StripHeadamp, 1, model.FieldGain), Value: level(t, 45), Origin: gesture}, model.ErrInvalidValue},
			{driver.Command{Param: ref(model.StripInput, 1, model.FieldName), Value: textOf(t, "A name far too long"), Origin: gesture}, model.ErrInvalidValue},
			{driver.Command{Param: ch1Fader, Value: model.Bool(true), Origin: gesture}, model.ErrInvalidValue},
		}
		for _, tc := range cases {
			if err := s.Apply(tc.cmd); !errors.Is(err, tc.want) {
				t.Errorf("Apply(%s, %v): %v, want %v", tc.cmd.Param, tc.cmd.Origin.Kind, err, tc.want)
			}
		}
		mustSet(t, r.sim, "/$syscfg/tcplock", 1)
		rn.changes(100 * time.Millisecond)
		if err := s.Apply(driver.Command{Param: ch1Fader, Value: level(t, 0), Origin: gesture}); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("locked console: %v", err)
		}
		mustSet(t, r.sim, "/$syscfg/tcplock", 0)
		rn.changes(100 * time.Millisecond)
		if err := s.Apply(driver.Command{Param: ch1Fader, Value: level(t, 0), Origin: gesture}); err != nil {
			t.Fatalf("unlocked console: %v", err)
		}
		rn.changes(time.Second)
		if w := r.sim.Writes(); len(w) != 1 {
			t.Fatalf("only the one accepted write may reach the console: %+v", w)
		}
	})
}

func textOf(t *testing.T, s string) model.Value {
	t.Helper()
	v, err := model.Text(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestGainAndName_Writes: Gain snaps to the console's grid (WG-F62); a
// name is written to name and read back from $name (WG-F107).
func TestGainAndName_Writes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		gain := ref(model.StripHeadamp, 1, model.FieldGain)
		name := ref(model.StripInput, 2, model.FieldName)
		_ = rn.s.Apply(driver.Command{Param: gain, Value: level(t, 11), Origin: gesture})
		_ = rn.s.Apply(driver.Command{Param: name, Value: textOf(t, "Kick"), Origin: gesture})
		cs := rn.changes(time.Second)
		if g, _ := r.sim.Value("/io/in/LCL/1/g"); g != 10.0 {
			t.Fatalf("gain %v, want the 2.5 dB grid value 10", g)
		}
		if n, _ := r.sim.Value("/ch/2/$name"); n != "Kick" {
			t.Fatalf("$name %v", n)
		}
		for _, p := range []model.ParamRef{gain, name} {
			if c, ok := findChange(cs, p); !ok || !c.OwnWrite || !c.Authoritative {
				t.Fatalf("%s not settled as our own: %+v", p, c)
			}
		}
		if a := r.audits(); len(a) != 2 || a[1].Address != "/ch/2/name" {
			t.Fatalf("audit %+v", a)
		}
	})
}

// TestIdleSession_NoWritesForAnHour: rule 2; probes keep the connection
// (WG-F25) and nothing is written.
func TestIdleSession_NoWritesForAnHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		s := r.connect()
		rn := r.run(s)
		_ = s.SetMeters(driver.MeterRequest{Inputs: true})
		_ = s.Reread()
		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case err := <-rn.done:
			t.Fatalf("session ended: %v", err)
		default:
		}
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("an idle hour wrote: %+v", w)
		}
		if r.sim.Connections() != 1 {
			t.Fatal("the console closed the idle connection")
		}
	})
}

// TestReconnect_DropsQueuedWritesAndReusesSchema: rule 3. After a drop the
// link's next session reads the state (changed during the outage) without
// walking the schema again, and nothing is replayed.
func TestReconnect_DropsQueuedWritesAndReusesSchema(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		l, err := r.drv.OpenLink(t.Context(), driver.Endpoint{Host: consoleIP}, r.opts())
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		sess, err := l.Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		rn := r.run(sess.(*session))
		r.sim.DropConnections()
		if err := <-rn.done; !errors.Is(err, driver.ErrConnectionLost) {
			t.Fatalf("drop: %v", err)
		}
		rn.done <- nil
		if err := sess.Apply(driver.Command{Param: ch1Fader, Value: level(t, 5), Origin: gesture}); !errors.Is(err, driver.ErrClosed) {
			t.Fatalf("Apply after loss: %v", err)
		}
		mustSet(t, r.sim, "/ch/1/fdr", -7.0)
		defsBefore := countClass(r.sim, simwing.ClassDefs)
		sess2, err := l.Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if st, _ := sess2.Snapshot().Strip(model.StripRef{Kind: model.StripInput, Index: 1}); st.FaderDB != -7 {
			t.Fatalf("reconnect read %v, want the console's -7", st.FaderDB)
		}
		if countClass(r.sim, simwing.ClassDefs) != defsBefore+1 { // one /ch request verifies the cache (review M2)
			t.Fatal("same firmware: the schema must not be walked again")
		}
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("writes replayed: %+v", w)
		}
		_ = sess2.Close()
	})
}

func countClass(s *simwing.Sim, c simwing.Class) int {
	n := 0
	for _, e := range s.Log() {
		if e.Class == c {
			n++
		}
	}
	return n
}
