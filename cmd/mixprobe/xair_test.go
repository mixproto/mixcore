// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

// xairLab is an X Air simulator prepared like a mixer someone left on.
type xairLab struct {
	net  *memnet.Network
	sim  *simxair.Sim
	snap map[string]osc.Arg
}

func newXAirLab(t *testing.T, opts simxair.Options) *xairLab {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":10024")
	if err != nil {
		t.Fatal(err)
	}
	opts.Name, opts.Model = "XR18-LAB", "XR18"
	sim := simxair.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	l := &xairLab{net: n, sim: sim, snap: map[string]osc.Arg{}}
	for addr, a := range map[string]osc.Arg{
		"/ch/01/mix/fader": osc.Float(0.75), "/ch/02/mix/fader": osc.Float(0.5), "/ch/03/mix/on": osc.Int(0),
		"/ch/02/grp/mute": osc.Int(1 << 3), "/ch/01/mix/01/level": osc.Float(0.75), "/ch/08/config/name": osc.String("Keys"),
	} {
		if err := sim.SurfaceSet(addr, a); err != nil {
			t.Fatal(err)
		}
	}
	scene := map[string]osc.Arg{}
	for ch := 9; ch <= 16; ch++ { // a lab snapshot that changes channels mixprobe does not own
		scene[chAddr(ch, "mix/fader")] = osc.Float(0.25)
	}
	scene["/ch/09/mix/on"], scene["/ch/10/mix/on"] = osc.Int(0), osc.Int(0)
	if err := sim.StoreSnapshot(5, "Lab", scene); err != nil {
		t.Fatal(err)
	}
	for _, addr := range xairAllowed() {
		l.snap[addr], _ = sim.Value(addr)
	}
	return l
}

func xairAllowed() []string {
	out := []string{"/config/mute/4", "/ch/01/mix/01/level", "/ch/08/config/name"}
	for ch := 1; ch <= 8; ch++ {
		out = append(out, chAddr(ch, "mix/fader"), chAddr(ch, "mix/on"))
	}
	return out
}

func (l *xairLab) config(p Prompter, out io.Writer) config {
	return config{fam: families["xair"], console: netip.MustParseAddrPort(consoleIP + ":10024"), channels: []int{1, 2, 3, 4, 5, 6, 7, 8},
		muteGroup: 4, listen: l.net.Host(labIP), prompt: p, out: out}
}

// xairOperator acts on the simulator for each physical step.
type xairOperator struct{ lab *xairLab }

func (o *xairOperator) Step(_ context.Context, id, _ string) (bool, error) {
	sim := o.lab.sim
	switch id {
	case stepMoveFader: // another app moves it (no surface, XA-F74)
		return true, sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.4))
	case stepRecallScene:
		go func() {
			time.Sleep(time.Second)
			_ = sim.LoadSnapshot(5)
		}()
	case stepUnplug:
		o.lab.net.SetDown(consoleIP, true)
	case stepReplug:
		o.lab.net.SetDown(consoleIP, false)
	case stepReboot:
		sim.Reboot(20 * time.Second)
	}
	return true, nil
}

func (o *xairOperator) Ask(context.Context, string, string) (bool, bool, error) {
	v, _ := o.lab.sim.Value("/config/mute/4")
	return v.Int == 1, true, nil
}

// assertSafe: only allowed parameters, never louder than the snapshot, no
// snapshot action, and everything restored.
func (l *xairLab) assertSafe(t *testing.T) {
	t.Helper()
	for _, w := range l.sim.Writes() {
		snap, ok := l.snap[w.Address]
		if !ok {
			t.Fatalf("write to %s, which is not allowed", w.Address)
		}
		louder := (w.Address == "/config/mute/4" && w.Arg.Int < snap.Int) ||
			(strings.HasSuffix(w.Address, "/mix/on") && w.Arg.Int > snap.Int) ||
			((strings.HasSuffix(w.Address, "/fader") || strings.HasSuffix(w.Address, "/level")) && w.Arg.Float > snap.Float)
		if louder {
			t.Fatalf("write %s %v is louder than the snapshot %v", w.Address, w.Arg, snap)
		}
	}
	for addr, want := range l.snap {
		if got, _ := l.sim.Value(addr); !got.Equal(want) {
			t.Fatalf("%s = %v after the session, want the snapshot %v", addr, got, want)
		}
	}
	for _, e := range l.sim.Log() {
		if strings.HasPrefix(e.Address, "/-snap/") && e.Class != simxair.ClassRead {
			t.Fatalf("mixprobe sent %s %s", e.Class, e.Address)
		}
	}
}

func runXAirSession(t *testing.T, opts simxair.Options) (map[string]Finding, string) {
	t.Helper()
	l := newXAirLab(t, opts)
	dir := t.TempDir()
	if keep := os.Getenv("MIXPROBE_XAIR_REPORT_DIR"); keep != "" {
		dir = keep // keep the report of a simulator run for review
	}
	var out bytes.Buffer
	if err := runAndReport(context.Background(), l.config(&xairOperator{lab: l}, &out), dir); err != nil {
		t.Fatalf("session: %v\n%s", err, out.String())
	}
	l.assertSafe(t)
	return readReport(t, dir), out.String()
}

func TestXAirSession_EndToEndAgainstTheSimulator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got, out := runXAirSession(t, simxair.Options{})
		want := map[string]Status{
			"identify": Confirmed, "model-strings": Confirmed, "read-driver-addresses": Confirmed, "group-membership-addresses": Confirmed,
			"send-on-absent": Confirmed, "dsp-counts": Confirmed, "snapshot-index-address": Confirmed, "unknown-address": Confirmed,
			"write-socket-reads": Confirmed, "meters-bank1": Confirmed, "meters-form-s": Measured, "meters-form-siii": Measured,
			"socket-reuse": Confirmed, "echo-to-sender": Confirmed, "reply-to-set": Refuted, "xremotenfb": Measured,
			"unchanged-write": Measured, "fader-steps": Confirmed, "send-steps": Confirmed, "name-length": Measured,
			"mute-group-polarity": Confirmed, "mute-group-or-model": Confirmed, "push-clients": Confirmed, "ninth-client": Measured,
			"ap-mode": Skipped, "bad-writes": Skipped, "quiet-console-stays-live": Confirmed, "reconnect-cycles": Measured,
			"client-slots": Confirmed, "desk-move-ends-hold": Confirmed, "scene-recall-pushes": Measured,
			"scene-recall-engine": Confirmed, "stale-timing": Confirmed, "replug-live": Measured, "reboot-live": Measured,
		}
		for id, st := range want {
			if f, ok := got[id]; !ok || f.Status != st {
				t.Errorf("%s: %s (%s), want %s", id, f.Status, f.Detail, st)
			}
		}
		if f := got["fader-steps"]; len(f.Facts) == 0 || f.Facts[0] != "XA-F41" {
			t.Errorf("shared checks must cite xair.md facts: %v", f.Facts)
		}
		if strings.Contains(out, "X32-Edit") {
			t.Error("the xair session must not ask to close X32 apps")
		}
	})
}

// The hardware answers that would change the doc are reported as refuted.
func TestXAirSession_ReportsOtherHypotheses(t *testing.T) {
	for _, tc := range []struct {
		opts simxair.Options
		want map[string]Status
	}{
		{simxair.Options{NoEchoToSender: true}, map[string]Status{"echo-to-sender": Refuted, "reply-to-set": Refuted}},
		{simxair.Options{ReplyToSet: true}, map[string]Status{"echo-to-sender": Confirmed, "reply-to-set": Confirmed}},
		{simxair.Options{Buses: 4}, map[string]Status{"dsp-counts": Refuted}},
		{simxair.Options{EvictOldestWhenFull: true}, map[string]Status{"push-clients": Refuted}},
	} {
		t.Run(fmt.Sprintf("%+v", tc.opts), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				got, _ := runXAirSession(t, tc.opts)
				for id, st := range tc.want {
					if got[id].Status != st {
						t.Errorf("%s: %s (%s), want %s", id, got[id].Status, got[id].Detail, st)
					}
				}
			})
		})
	}
}

func TestParseArgs_Family(t *testing.T) {
	o, err := parseArgs([]string{"--lab", "-family", "xair", "-host", "192.168.1.1"}, io.Discard)
	if err != nil || o.port != 10024 || o.muteGroup != 4 || o.fam.name != "xair" {
		t.Fatalf("%+v %v", o, err)
	}
	for _, args := range [][]string{
		{"--lab", "-family", "xair", "-host", "1.2.3.4", "-channels", "1-17"},
		{"--lab", "-family", "xair", "-host", "1.2.3.4", "-mutegroup", "5"},
		{"--lab", "-family", "wing", "-host", "1.2.3.4"},
	} {
		if _, err := parseArgs(args, io.Discard); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
