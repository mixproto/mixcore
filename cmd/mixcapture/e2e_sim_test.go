// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/capture/law"
	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

const (
	e2eWindow    = time.Second
	e2eQuiet     = 700 * time.Millisecond
	readBatch    = 32
	renewEvery   = 2 * time.Second // /xremote lives 10 s (XA-F20)
	outputWait   = 10 * time.Second
	faderAddr    = "/ch/01/mix/fader"
	meterAddress = "/meters/1"
)

// fakeApp plays a vendor app: it talks OSC to the relay, renews /xremote,
// subscribes meters, reads the whole tree and moves a fader.
type fakeApp struct {
	t     *testing.T
	pc    net.PacketConn
	relay net.Addr
	mu    sync.Mutex
	got   map[string]int // replies per address
}

func (a *fakeApp) send(addr string, args ...osc.Arg) {
	a.t.Helper()
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.pc.WriteTo(b, a.relay); err != nil {
		a.t.Fatal(err)
	}
}

func (a *fakeApp) receive(ctx context.Context) {
	buf := make([]byte, 4096)
	for ctx.Err() == nil {
		_ = a.pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := a.pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		var m osc.Message
		if osc.Decode(buf[:n], &m) == nil {
			a.mu.Lock()
			a.got[string(m.Address)]++
			a.mu.Unlock()
		}
	}
}

func (a *fakeApp) replies(addr string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.got[addr]
}

// readAll reads every address in batches, waiting for each batch's replies.
func (a *fakeApp) readAll(addrs []string) {
	for i := 0; i < len(addrs); i += readBatch {
		batch := addrs[i:min(i+readBatch, len(addrs))]
		for _, addr := range batch {
			a.send(addr)
		}
		deadline := time.Now().Add(2 * time.Second)
		for _, addr := range batch {
			for a.replies(addr) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
		}
	}
}

func (a *fakeApp) renew(ctx context.Context) {
	t := time.NewTicker(renewEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.send("/xremote")
			a.send("/meters", osc.String(meterAddress))
		}
	}
}

// animateMeters makes the meter stream change all the time: noise that must
// never be reported as a candidate.
func animateMeters(ctx context.Context, sim *simxair.Sim) {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sim.SetMeterValue(rand.IntN(16), int16(-rand.IntN(30000))) //nolint:gosec // G404: test noise
		}
	}
}

type e2e struct {
	t      *testing.T
	sim    *simxair.Sim
	app    *fakeApp
	out    *syncBuf
	stdin  *io.PipeWriter
	dir    string
	pos    int
	ctx    context.Context
	done   chan error
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (e *e2e) cmd(line string) {
	e.t.Helper()
	if _, err := io.WriteString(e.stdin, line+"\n"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *e2e) wait(text string) { e.t.Helper(); e.pos = e.out.waitFor(e.t, text, e.pos, outputWait) }

var relayLine = regexp.MustCompile(`relay udp (127\.0\.0\.1:\d+) -> `)

func startE2E(t *testing.T) *e2e {
	t.Helper()
	simPC, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := &e2e{t: t, sim: simxair.New(simxair.Options{}), out: &syncBuf{}, dir: filepath.Join(t.TempDir(), "xr18", "2026-10-12"),
		done: make(chan error, 1)}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.wg.Go(func() { _ = e.sim.Serve(e.ctx, simPC) })
	e.wg.Go(func() { animateMeters(e.ctx, e.sim) })
	simPort := simPC.LocalAddr().(*net.UDPAddr).Port
	stdinR, stdinW := io.Pipe()
	e.stdin = stdinW
	args := []string{"relay", "-console", "127.0.0.1", "-listen", "127.0.0.1", "-app", "127.0.0.1", "-udp", fmt.Sprintf("0:%d", simPort),
		"-out", e.dir, "-window", e2eWindow.String(), "-quiet", e2eQuiet.String()}
	e.wg.Go(func() { e.done <- runMain(e.ctx, args, stdinR, e.out) })
	t.Cleanup(func() { e.cancel(); _ = stdinW.Close(); e.wg.Wait(); _ = simPC.Close() })
	e.wait("relay udp ")
	m := relayLine.FindStringSubmatch(e.out.String())
	if m == nil {
		t.Fatalf("no relay address in:\n%s", e.out.String())
	}
	relayAddr, err := net.ResolveUDPAddr("udp4", m[1])
	if err != nil {
		t.Fatal(err)
	}
	appPC, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appPC.Close() })
	e.app = &fakeApp{t: t, pc: appPC, relay: relayAddr, got: map[string]int{}}
	e.wg.Go(func() { e.app.receive(e.ctx) })
	return e
}

// faderSteps are four surface readings and the 1024-step wire values the
// app sends for them (docs/protocols/xair.md reference table, XA-F40/F41).
var faderSteps = []struct {
	db float64
	k  int
}{{-60, 64}, {-30, 256}, {-10, 512}, {10, 1023}}

// TestE2E_RelayMarksSnapshotsHarvestAndLaw drives the whole tool against the
// X Air simulator as the console, with a scripted vendor app behind the relay.
func TestE2E_RelayMarksSnapshotsHarvestAndLaw(t *testing.T) {
	e := startE2E(t)
	addrs := e.sim.Addresses()
	if err := e.sim.StoreSnapshot(2, "Song B", map[string]osc.Arg{
		faderAddr: osc.Float(0.25), "/ch/02/mix/on": osc.Int(0), "/bus/1/mix/fader": osc.Float(0.6)}); err != nil {
		t.Fatal(err)
	}

	// 1. Connect like a vendor app: identify, subscribe, read the whole tree.
	e.app.send("/xinfo")
	e.app.send("/xremote")
	e.app.send("/meters", osc.String(meterAddress))
	e.wg.Go(func() { e.app.renew(e.ctx) })
	e.app.readAll(addrs)
	e.wait("harvest 1: initial sync from 127.0.0.1:")
	e.cmd(":s before")
	e.wait(`snapshot 1 "before"`)

	// 2. Marks with law points while the app moves channel 1's fader.
	for i, s := range faderSteps {
		label := fmt.Sprintf("ch1 fader = %g dB", s.db)
		e.cmd(label)
		e.wait(fmt.Sprintf("mark %d %q", i+1, label))
		e.app.send(faderAddr, osc.Float(float32(float64(s.k)/1023)))
		e.wait(fmt.Sprintf("mark %d %q closed: 1 candidate(s)", i+1, label))
		e.wait("1. " + faderAddr + " ")
	}

	// 3. A snapshot recall made elsewhere, then the diff.
	if err := e.sim.LoadSnapshot(2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	e.cmd(":s after recall")
	e.wait("diff snapshot 1 -> 2: 3 change(s), 1 streaming key(s) ignored")
	e.cmd(":law " + faderAddr)
	e.wait("law " + faderAddr + ": piecewise linear, 3 segment(s)")
	e.cmd(":q")
	select {
	case err := <-e.done:
		if err != nil {
			t.Fatalf("relay session: %v\n%s", err, e.out.String())
		}
	case <-time.After(outputWait):
		t.Fatalf("relay session did not end after :q\n%s", e.out.String())
	}
	checkE2EOutputs(t, e, addrs)
}

func checkE2EOutputs(t *testing.T, e *e2e, addrs []string) {
	t.Helper()
	// Live output: candidates for each mark; the animated meters never show up as a real candidate.
	if out := e.out.String(); strings.Contains(out, "candidate: "+meterAddress) {
		t.Errorf("meter noise reported as a candidate:\n%s", out)
	}
	for _, want := range []string{"/ch/01/mix/fader 0→0.25024438", "/ch/02/mix/on 1→0", "/bus/1/mix/fader 0→0.6001955"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("diff lacks %q", want)
		}
	}
	// The harvest lists the sim's full tree with values.
	harvest := readFile(t, filepath.Join(e.dir, "harvest.csv"))
	for _, a := range addrs {
		if !strings.Contains(harvest, ","+a+",osc,") {
			t.Fatalf("harvest.csv lacks %s", a)
		}
	}
	// The law helper recovers the documented fader law from the four marks.
	var af analysisFlags
	af.window, af.quiet = e2eWindow, e2eQuiet
	pts, err := sessionLawPoints(e.dir, &af, faderAddr)
	if err != nil || len(pts) != len(faderSteps) {
		t.Fatalf("law points = %v, %v", pts, err)
	}
	best, _, err := law.Fit(pts, law.DefaultTolerance)
	if err != nil || best.Kind != law.Piecewise {
		t.Fatalf("law = %s, %v", best, err)
	}
	for _, f := range []float64{0.0625, 0.125, 0.1875, 0.25, 0.375, 0.5, 0.625, 0.75, 0.875, 1} {
		if got, want := best.Eval(f), xairDB(f); math.Abs(got-want) > 0.05 {
			t.Errorf("recovered law at %v = %.3f dB, documented %.3f dB", f, got, want)
		}
	}
	// Safety: the console received exactly the app's four fader writes and nothing the tool made up.
	writes := e.sim.Writes()
	if len(writes) != len(faderSteps) {
		t.Fatalf("console received %d writes, the app sent %d", len(writes), len(faderSteps))
	}
	for _, w := range writes {
		if w.Address != faderAddr {
			t.Fatalf("unexpected write to %s", w.Address)
		}
	}
	checkSessionFiles(t, e.dir)
}

func checkSessionFiles(t *testing.T, dir string) {
	t.Helper()
	md := readFile(t, filepath.Join(dir, "report.md"))
	for _, want := range []string{"**Privacy.**", "### Mark 4: \"ch1 fader = 10 dB\"", "| `/ch/01/mix/fader` | 0.50048876→1 |",
		"## Candidate value laws", "## Initial-sync harvest", "1 streaming key(s) left out"} {
		if !strings.Contains(md, want) {
			t.Errorf("report.md lacks %q", want)
		}
	}
	for _, f := range []string{"capture.pcap", "index.jsonl", "session.json", "keys.csv", "marks.csv"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Size() == 0 {
			t.Errorf("%s missing or empty: %v", f, err)
		}
	}
	before := readFile(t, filepath.Join(dir, "marks.csv"))
	if _, err := runCmd(t, "report", "-session", dir, "-window", e2eWindow.String(), "-quiet", e2eQuiet.String()); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, filepath.Join(dir, "marks.csv")); after != before {
		t.Errorf("re-analysis of the recorded session changed marks.csv:\n%s\nlive:\n%s", after, before)
	}
}

// xairDB is the documented X Air level law (docs/protocols/xair.md, XA-F40).
func xairDB(f float64) float64 {
	switch {
	case f <= 0:
		return math.Inf(-1)
	case f >= 0.5:
		return 40*f - 30
	case f >= 0.25:
		return 80*f - 50
	case f >= 0.0625:
		return 160*f - 70
	default:
		return 480*f - 90
	}
}
