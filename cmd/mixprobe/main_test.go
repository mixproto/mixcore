// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	consoleIP = "10.0.0.1"
	labIP     = "10.0.0.2"
)

// lab is a simulated console on an in-memory network, prepared like a desk
// an engineer left: some faders up, one channel muted, ch 2 in mute group 6.
type lab struct {
	net  *memnet.Network
	sim  *simx32.Sim
	snap map[string]osc.Arg // values before the session
}

func newLab(t *testing.T, opts simx32.Options) *lab {
	t.Helper()
	n := memnet.New()
	pc, err := n.ListenPacket(consoleIP + ":10023")
	if err != nil {
		t.Fatal(err)
	}
	opts.Name = "LAB"
	sim := simx32.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	l := &lab{net: n, sim: sim, snap: map[string]osc.Arg{}}
	for addr, a := range map[string]osc.Arg{
		"/ch/01/mix/fader": osc.Float(0.75), "/ch/02/mix/fader": osc.Float(0.5), "/ch/03/mix/on": osc.Int(0),
		"/ch/02/grp/mute": osc.Int(1 << 5),
	} {
		if err := sim.SurfaceSet(addr, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, addr := range allowedAddrs() {
		l.snap[addr], _ = sim.Value(addr)
	}
	return l
}

func allowedAddrs() []string {
	var out []string
	for ch := 1; ch <= 8; ch++ {
		out = append(out, chAddr(ch, "mix/fader"), chAddr(ch, "mix/on"))
	}
	return append(out, "/config/mute/6")
}

func (l *lab) config(p Prompter, out io.Writer) config {
	return config{fam: families["x32"], console: netip.MustParseAddrPort(consoleIP + ":10023"), channels: []int{1, 2, 3, 4, 5, 6, 7, 8}, muteGroup: 6,
		listen: l.net.Host(labIP), prompt: p, out: out}
}

// operator answers the physical steps by acting on the simulator.
type operator struct {
	lab    *lab
	cancel context.CancelFunc // set: Ctrl-C at stopAt
	stopAt string
	mu     sync.Mutex
	steps  []string
}

func (o *operator) Step(ctx context.Context, id, _ string) (bool, error) {
	o.mu.Lock()
	o.steps = append(o.steps, id)
	o.mu.Unlock()
	if id == o.stopAt {
		o.cancel()
		return false, ctx.Err()
	}
	sim := o.lab.sim
	switch id {
	case stepMoveFader:
		return true, sim.SurfaceSet("/ch/01/mix/fader", osc.Float(0.4))
	case stepRecallScene:
		go func() { // the operator recalls a moment after pressing Enter
			time.Sleep(time.Second)
			_ = sim.RecallScene(3, sceneValues())
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

func (o *operator) Ask(_ context.Context, id, _ string) (bool, bool, error) {
	o.mu.Lock()
	o.steps = append(o.steps, id)
	o.mu.Unlock()
	v, _ := o.lab.sim.Value("/config/mute/6")
	return v.Int == 1, true, nil
}

func sceneValues() map[string]osc.Arg {
	out := map[string]osc.Arg{}
	for ch := 1; ch <= 16; ch++ {
		out[chAddr(ch, "mix/fader")] = osc.Float(0.25)
	}
	return out
}

// assertSafeWrites checks every write the console received: only allowed
// parameters, never louder than the snapshot.
func (l *lab) assertSafeWrites(t *testing.T) {
	t.Helper()
	for _, w := range l.sim.Writes() {
		snap, ok := l.snap[w.Address]
		if !ok {
			t.Fatalf("write to %s, which is not allowed", w.Address)
		}
		louder := (w.Address == "/config/mute/6" && w.Arg.Int < snap.Int) ||
			(strings.HasSuffix(w.Address, "/mix/on") && w.Arg.Int > snap.Int) ||
			(strings.HasSuffix(w.Address, "/fader") && w.Arg.Float > snap.Float)
		if louder {
			t.Fatalf("write %s %v is louder than the snapshot %v", w.Address, w.Arg, snap)
		}
	}
}

func (l *lab) assertRestored(t *testing.T) {
	t.Helper()
	for addr, want := range l.snap {
		if got, _ := l.sim.Value(addr); !got.Equal(want) {
			t.Fatalf("%s = %v after the session, want the snapshot %v", addr, got, want)
		}
	}
}

func readReport(t *testing.T, dir string) map[string]Finding {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "report.json")) //nolint:gosec // G304: the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	var r struct{ Findings []Finding }
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	out := map[string]Finding{}
	for _, f := range r.Findings {
		out[f.ID] = f
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil { //nolint:gosec // G703: the test's own temp dir
		t.Fatal(err)
	}
	return out
}

func TestSession_EndToEndAgainstTheSimulator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab(t, simx32.Options{})
		dir := t.TempDir()
		if keep := os.Getenv("MIXPROBE_REPORT_DIR"); keep != "" {
			dir = keep // keep the report of a simulator run for review
		}
		var out bytes.Buffer
		op := &operator{lab: l}
		if err := runAndReport(context.Background(), l.config(op, &out), dir); err != nil {
			t.Fatalf("session: %v\n%s", err, out.String())
		}
		got := readReport(t, dir)
		for id, want := range map[string]Status{
			"identify": Confirmed, "read-X32-F35": Confirmed, "read-X32-F36": Confirmed, "read-X32-F37": Confirmed,
			"read-X32-F38": Confirmed, "read-X32-F61": Confirmed, "read-X32-F62": Confirmed, "unknown-address": Confirmed,
			"write-socket-reads": Confirmed, "push-to-xremote": Confirmed, "unchanged-write": Measured,
			"no-echo-to-sender": Confirmed, "fader-steps": Confirmed, "mute-group-polarity": Confirmed,
			"mute-group-or-model": Confirmed, "meters-bank1": Confirmed, "meters-bank-name": Measured, "meters-bank2": Confirmed,
			"quiet-console-stays-live": Confirmed, "reconnect-cycles": Measured, "client-slots": Confirmed,
			"desk-move-ends-hold": Confirmed, "scene-recall-pushes": Measured, "scene-recall-engine": Confirmed, "stale-timing": Confirmed,
			"replug-live": Measured, "reboot-live": Measured,
		} {
			if f, ok := got[id]; !ok || f.Status != want {
				t.Errorf("%s: %s (%s), want %s", id, f.Status, f.Detail, want)
			}
		}
		l.assertSafeWrites(t)
		l.assertRestored(t)
		if _, err := os.Stat(filepath.Join(dir, "snapshot.json")); err != nil { //nolint:gosec // G703: the test's own temp dir
			t.Fatalf("the snapshot must be on disk: %v", err)
		}
	})
}

// Ctrl-C in the middle of the session still restores the snapshot.
func TestSession_InterruptRestoresTheSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab(t, simx32.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir := t.TempDir()
		op := &operator{lab: l, cancel: cancel, stopAt: stepMoveFader}
		err := runAndReport(ctx, l.config(op, io.Discard), dir)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want the cancellation", err)
		}
		if got := readReport(t, dir); got["push-to-xremote"].Status != Confirmed {
			t.Fatal("checks before the interrupt must be in the report")
		}
		l.assertSafeWrites(t)
		l.assertRestored(t)
	})
}

// The pessimistic slot model and the evict-oldest policy are reported as refuted.
func TestSlotFinding_Patterns(t *testing.T) {
	for _, tc := range []struct {
		served []bool
		want   Status
		says   string
	}{
		{[]bool{true, true, true, true, false}, Confirmed, "newcomer ignored"},
		{[]bool{true, true, true, false, false}, Refuted, "write socket takes a slot"},
		{[]bool{false, true, true, true, true}, Refuted, "evict"},
		{[]bool{true, true, true, true, true}, Refuted, "more than 4"},
		{[]bool{false, false, false, false, false}, Inconclusive, ""},
	} {
		f := slotFinding(tc.served, 4)
		if f.Status != tc.want || !strings.Contains(f.Detail, tc.says) {
			t.Errorf("%v: %s %q", tc.served, f.Status, f.Detail)
		}
	}
}

func TestParseArgs_SafetyAndChannels(t *testing.T) {
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"-host", "192.168.1.62"}, false}, // no --lab
		{[]string{"--lab", "-host", "192.168.1.62"}, true},
		{[]string{"--lab"}, false},
		{[]string{"--lab", "-host", "1.2.3.4", "-discover"}, false},
		{[]string{"--lab", "-host", "1.2.3.4", "-channels", "0-8"}, false},
		{[]string{"--lab", "-host", "1.2.3.4", "-channels", "1,3,30-32"}, true},
		{[]string{"--lab", "-host", "1.2.3.4", "-mutegroup", "7"}, false},
	} {
		_, err := parseArgs(tc.args, io.Discard)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, errUsage)) {
			t.Errorf("%v: err = %v", tc.args, err)
		}
	}
	chs, _ := parseChannels("1,3,30-32", 32)
	if len(chs) != 5 || chs[4] != 32 {
		t.Fatalf("channels = %v", chs)
	}
	if err := runMain(context.Background(), []string{"-host", "1.2.3.4"}, strings.NewReader(""), io.Discard, nil); !errors.Is(err, errUsage) {
		t.Fatalf("runMain without --lab: %v", err)
	}
}

func TestGuard_RefusesLouderAndUnknownWrites(t *testing.T) {
	g := &guard{params: []*guarded{
		{addr: "/ch/01/mix/fader", dir: downOnly, snap: osc.Float(0.5), read: true},
		{addr: "/ch/01/mix/on", dir: downOnly, snap: osc.Int(0), read: true},
		{addr: "/config/mute/6", dir: upOnly, snap: osc.Int(1), read: true},
		{addr: "/ch/02/mix/on", dir: downOnly},
	}}
	for _, tc := range []struct {
		addr string
		a    osc.Arg
		want error
	}{
		{"/ch/01/mix/fader", osc.Float(0.51), errUnsafe}, {"/ch/01/mix/fader", osc.Float(0.25), nil},
		{"/ch/01/mix/on", osc.Int(1), errUnsafe}, {"/config/mute/6", osc.Int(0), errUnsafe},
		{"/ch/09/mix/on", osc.Int(0), errNotAllowed}, {"/ch/02/mix/on", osc.Int(0), errNoSnapshot},
		{"/ch/01/mix/fader", osc.Int(0), errUnsafe},
		{"/ch/01/mix/on", osc.Int(-1), errUnsafe}, {"/config/mute/6", osc.Int(5), errUnsafe},
		{"/ch/01/mix/fader", osc.Float(-0.1), errUnsafe},
	} {
		if err := g.check(tc.addr, tc.a); !errors.Is(err, tc.want) {
			t.Errorf("%s %v: err = %v, want %v", tc.addr, tc.a, err, tc.want)
		}
	}
}

// skipper skips every physical step and question.
type skipper struct{}

func (skipper) Step(_ context.Context, id, _ string) (bool, error) { return id == stepAmpsOff, nil }
func (skipper) Ask(context.Context, string, string) (bool, bool, error) {
	return false, false, nil
}

// A desk with everything muted, every fader down and mute group 6 engaged
// leaves nothing mixprobe may move: those checks are skipped, nothing is written.
func TestSession_NothingToMoveAndEveryStepSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab(t, simx32.Options{})
		for ch := 1; ch <= 8; ch++ {
			_ = l.sim.SurfaceSet(chAddr(ch, "mix/on"), osc.Int(0))
			_ = l.sim.SurfaceSet(chAddr(ch, "mix/fader"), osc.Float(0))
		}
		_ = l.sim.SurfaceSet("/config/mute/6", osc.Int(1))
		for _, addr := range allowedAddrs() {
			l.snap[addr], _ = l.sim.Value(addr)
		}
		dir := t.TempDir()
		if err := runAndReport(context.Background(), l.config(skipper{}, io.Discard), dir); err != nil {
			t.Fatal(err)
		}
		got := readReport(t, dir)
		for _, id := range []string{"push-and-echo", "fader-steps", "mute-group", "client-slots", "desk-move-ends-hold",
			"scene-recall", "stale-timing", "reboot"} {
			if got[id].Status != Skipped {
				t.Errorf("%s: %s, want skipped", id, got[id].Status)
			}
		}
		if n := len(l.sim.Writes()); n != 0 {
			t.Fatalf("%d writes on a desk with nothing to move", n)
		}
	})
}

// A console that does not answer aborts before anything is written.
func TestSession_NoConsoleAbortsWithoutWriting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab(t, simx32.Options{})
		l.sim.Pause()
		err := runAndReport(context.Background(), l.config(&operator{lab: l}, io.Discard), t.TempDir())
		if !errors.Is(err, errAborted) || len(l.sim.Writes()) != 0 {
			t.Fatalf("err = %v, writes %d", err, len(l.sim.Writes()))
		}
	})
}

func TestTerminal_StepsAndQuestions(t *testing.T) {
	var out bytes.Buffer
	term := newTerminal(strings.NewReader("\ns\nmaybe\ny\nn\ns\n"), &out)
	ctx := context.Background()
	if done, err := term.Step(ctx, "a", "do it"); !done || err != nil {
		t.Fatalf("Enter: %v %v", done, err)
	}
	if done, _ := term.Step(ctx, "b", "do it"); done {
		t.Fatal("s must skip")
	}
	if yes, answered, _ := term.Ask(ctx, "c", "lit?"); !yes || !answered {
		t.Fatal("y after an invalid answer")
	}
	if yes, answered, _ := term.Ask(ctx, "d", "lit?"); yes || !answered {
		t.Fatal("n")
	}
	if _, answered, _ := term.Ask(ctx, "e", "lit?"); answered {
		t.Fatal("s must skip the question")
	}
	if _, err := term.Step(ctx, "f", "end of input"); !errors.Is(err, io.EOF) {
		t.Fatalf("closed input: %v", err)
	}
	if !strings.Contains(out.String(), "[a] do it") {
		t.Fatalf("prompt text missing: %q", out.String())
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := newTerminal(strings.NewReader(""), io.Discard).Step(cancelled, "g", "x"); err == nil {
		t.Fatal("a cancelled prompt must return an error")
	}
}

func TestFindConsole_DiscoverAndHost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab(t, simx32.Options{})
		ap, err := findConsole(context.Background(), options{fam: families["x32"], discover: true}, l.net.Host(labIP), io.Discard)
		if err != nil || ap.String() != consoleIP+":10023" {
			t.Fatalf("discover: %v %v", ap, err)
		}
		if _, err := findConsole(context.Background(), options{fam: families["x32"], host: "x32.local", port: 10023}, nil, io.Discard); !errors.Is(err, errUsage) {
			t.Fatalf("hostname: %v", err)
		}
		l.sim.Pause()
		if _, err := findConsole(context.Background(), options{fam: families["x32"], discover: true}, l.net.Host(labIP), io.Discard); !errors.Is(err, errUsage) {
			t.Fatalf("nothing found: %v", err)
		}
	})
}

// A restore that cannot be verified (the console stopped answering) is
// retried and then reported as not restored, never as done.
func TestRestore_ReportsWhatCouldNotBeVerified(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab(t, simx32.Options{})
		w, err := openRaw(context.Background(), l.net.Host(labIP), netip.MustParseAddrPort(consoleIP+":10023"))
		if err != nil {
			t.Fatal(err)
		}
		defer w.close()
		var out bytes.Buffer
		p := &prober{cfg: config{out: &out}, guard: newGuard(w, config{fam: families["x32"], channels: []int{1}, muteGroup: 6})}
		if err := p.guard.snapshot(replyWait); err != nil {
			t.Fatal(err)
		}
		if err := p.guard.write("/ch/01/mix/on", osc.Int(0)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		l.sim.Pause()
		res := p.restore()
		if len(res) != 1 || res[0].OK || !strings.Contains(out.String(), "NOT RESTORED") {
			t.Fatalf("results %+v, output %q", res, out.String())
		}
		l.sim.Resume()
		if res := p.restore(); len(res) != 1 || !res[0].OK {
			t.Fatalf("second restore: %+v", res)
		}
	})
}
