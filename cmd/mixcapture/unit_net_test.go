// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

const (
	memConsole = "10.0.0.1"
	memRelay   = "10.0.0.9"
	memApp     = "10.0.0.2"
)

// memRig is an in-memory network: UDP on memnet, TCP over pipes to a fakeRCP.
type memRig struct {
	n   *memnet.Network
	rcp *fakeRCP
	lns chan *chanListener
	wg  sync.WaitGroup
}

// chanListener hands out the server ends of pipes made by dialFrom.
type chanListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{IP: net.ParseIP(memRelay), Port: 49280} }

type fromConn struct {
	net.Conn
	from net.Addr
}

func (c fromConn) RemoteAddr() net.Addr { return c.from }

func (l *chanListener) dialFrom(from string) net.Conn {
	client, server := net.Pipe()
	l.conns <- fromConn{Conn: server, from: net.TCPAddrFromAddrPort(netip.MustParseAddrPort(from))}
	return client
}

func newMemRig(t *testing.T) *memRig {
	t.Helper()
	r := &memRig{n: memnet.New(), rcp: &fakeRCP{}, lns: make(chan *chanListener, 1)}
	t.Cleanup(r.wg.Wait)
	return r
}

func (r *memRig) ops() netOps {
	return netOps{
		listenPacket: func(_ context.Context, _, address string) (net.PacketConn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
				host = memRelay
			}
			return r.n.ListenPacket(net.JoinHostPort(host, port))
		},
		listen: func(context.Context, string, string) (net.Listener, error) {
			ln := &chanListener{conns: make(chan net.Conn), closed: make(chan struct{})}
			r.lns <- ln
			return ln, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			r.wg.Go(func() { r.rcp.serve(server) })
			return client, nil
		},
		isLocal: func(ip netip.Addr) bool { return ip == netip.MustParseAddr(memRelay) },
	}
}

type memClient struct {
	t  *testing.T
	pc net.PacketConn
	to net.Addr
}

func (a memClient) send(addr string, args ...osc.Arg) {
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.pc.WriteTo(b, a.to); err != nil {
		a.t.Fatal(err)
	}
}

// drain keeps the app's socket empty, as a real app reads its replies.
func (a memClient) drain(ctx context.Context) {
	buf := make([]byte, 4096)
	for ctx.Err() == nil {
		_ = a.pc.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		_, _, _ = a.pc.ReadFrom(buf)
	}
}

// relayRig is a relay command running on the in-memory network, with the
// X Air simulator as the UDP console and the fake RCP console over TCP.
type relayRig struct {
	t     *testing.T
	r     *memRig
	sim   *simxair.Sim
	out   *syncBuf
	stdin *io.PipeWriter
	dir   string
	done  chan error
	pos   int
	ctx   context.Context
}

func startMemRelay(t *testing.T) *relayRig {
	t.Helper()
	m := &relayRig{t: t, r: newMemRig(t), sim: simxair.New(simxair.Options{}), out: &syncBuf{},
		dir: filepath.Join(t.TempDir(), "s"), done: make(chan error, 1)}
	simPC, err := m.r.n.ListenPacket(memConsole + ":10024")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	stdinR, stdinW := io.Pipe()
	m.stdin = stdinW
	t.Cleanup(func() { cancel(); _ = stdinW.Close(); m.r.wg.Wait(); _ = simPC.Close() })
	m.r.wg.Go(func() { _ = m.sim.Serve(ctx, simPC) })
	m.r.wg.Go(func() {
		m.done <- runWith(ctx, []string{"relay", "-console", memConsole, "-listen", memRelay, "-app", memApp,
			"-udp", "19024:10024", "-tcp", "49280", "-window", "300ms", "-quiet", "300ms", "-out", m.dir}, stdinR, m.out, m.r.ops())
	})
	m.wait("recording;")
	return m
}

func (m *relayRig) wait(text string) {
	m.t.Helper()
	m.pos = m.out.waitFor(m.t, text, m.pos, outputWait)
}

// say types a terminal line and waits for its output.
func (m *relayRig) say(line, want string) {
	m.t.Helper()
	if _, err := io.WriteString(m.stdin, line+"\n"); err != nil {
		m.t.Fatal(err)
	}
	m.wait(want)
}

// connectApps plays the vendor apps: OSC reads over UDP, one RCP get over TCP.
func (m *relayRig) connectApps() memClient {
	m.t.Helper()
	ln := <-m.r.lns
	pc, err := m.r.n.ListenPacket(memApp + ":0")
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(func() { _ = pc.Close() })
	app := memClient{t: m.t, pc: pc, to: net.UDPAddrFromAddrPort(netip.MustParseAddrPort(memRelay + ":19024"))}
	m.r.wg.Go(func() { app.drain(m.ctx) })
	for _, a := range []string{"/xinfo", "/xremote", "/ch/01/mix/fader", "/ch/01/mix/on", "/ch/02/mix/fader"} {
		app.send(a)
	}
	tcpApp := ln.dialFrom(memApp + ":40000")
	m.t.Cleanup(func() { _ = tcpApp.Close() })
	if _, err := io.WriteString(tcpApp, "get MIXER:Current/InCh/Fader/Level 0 0\n"); err != nil {
		m.t.Fatal(err)
	}
	if _, err := bufio.NewReader(tcpApp).ReadString('\n'); err != nil {
		m.t.Fatal(err)
	}
	return app
}

// TestRelay_UnitMemnet drives the relay command over the in-memory network
// with every terminal command, and checks that only the apps' own traffic
// reaches the consoles.
func TestRelay_UnitMemnet(t *testing.T) {
	m := startMemRelay(t)
	app := m.connectApps()
	m.wait("harvest 1:")
	m.say("", `mark 1 "mark 1"`)
	m.say(":s a", `snapshot 1 "a"`)
	for i, step := range []struct {
		db float64
		f  float32
	}{{-10, 0.5}, {0, 0.75}} {
		label := fmt.Sprintf("ch1 fader = %g dB", step.db)
		m.say(label, fmt.Sprintf("mark %d %q", i+2, label))
		app.send("/ch/01/mix/fader", osc.Float(step.f))
		m.wait(fmt.Sprintf("mark %d %q closed: 1 candidate(s)", i+2, label))
	}
	m.say(":law /ch/01/mix/fader", "law /ch/01/mix/fader: linear: y = 40")
	m.say(":law nope", `law: no key "nope"`)
	m.say(":keys /ch/01/mix/f", "  /ch/01/mix/fader = ")
	m.say(":harvest", "harvest 1 from 10.0.0.2:")
	m.say(":help", "commands while recording:")
	m.say(":s b", "diff snapshot 1 -> 2: 1 change(s)")
	m.say(":q", "report: ")
	if err := <-m.done; err != nil {
		t.Fatalf("relay: %v\n%s", err, m.out.String())
	}
	if w := m.sim.Writes(); len(w) != 2 {
		t.Fatalf("console received %d writes, the app sent 2", len(w))
	}
	if got := m.r.rcp.received(); len(got) != 1 || got[0] != "get MIXER:Current/InCh/Fader/Level 0 0" {
		t.Fatalf("TCP console received %q", got)
	}
	if keys := readFile(t, filepath.Join(m.dir, "keys.csv")); !strings.Contains(keys, "MIXER:Current/InCh/Fader/Level 0 0,text,text,,both,") {
		t.Fatalf("keys.csv lacks the TCP text key:\n%s", keys)
	}
}

func TestIntrospect_UnitPipe(t *testing.T) {
	r := newMemRig(t)
	dir := filepath.Join(t.TempDir(), "tf5")
	var out syncBuf
	err := runWith(context.Background(), []string{"introspect", "-family", "yamaha-rcp", "-console", memConsole,
		"-out", dir}, strings.NewReader(""), &out, r.ops())
	if err != nil {
		t.Fatalf("introspect: %v\n%s", err, out.String())
	}
	if p := readFile(t, filepath.Join(dir, "params.csv")); !strings.Contains(p, "0,MIXER:Current/InCh/Fader/Level,40,1,") {
		t.Fatalf("params.csv:\n%s", p)
	}
	if !strings.Contains(out.String(), "devinfo productname: TF5") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestLaw_FromSessionMarks(t *testing.T) {
	dir := t.TempDir()
	lines := strings.SplitAfter(readFile(t, "../../internal/capture/decode/testdata/json-lines.txt"), "\n")
	var chunks [][]byte
	for _, l := range lines {
		if l != "" {
			chunks = append(chunks, []byte(l))
		}
	}
	pcap := filepath.Join(dir, "ui.pcap")
	writePcap(t, pcap, chunks)
	writeFile(t, filepath.Join(dir, "marks.txt"), "+0.1 vol = -10 dB\n+0.35 vol = 0 dB\n")
	out := filepath.Join(dir, "session")
	if got, err := runCmd(t, "read", "-pcap", pcap, "-console", "192.168.1.62", "-ports", "51325", "-window", "100ms",
		"-marks", filepath.Join(dir, "marks.txt"), "-out", out); err != nil {
		t.Fatalf("read: %v\n%s", err, got)
	}
	got, err := runCmd(t, "law", "-session", out, "-window", "100ms", "-key", "line/ch1/volume", "-p", "1=10")
	if err != nil || !strings.Contains(got, "best: linear: y = 40*x - 30") {
		t.Fatalf("law: %v\n%s", err, got)
	}
	if _, err := runCmd(t, "law", "-session", out, "-key", "nope"); err == nil {
		t.Fatal("law with an unknown key succeeded")
	}
	if _, err := runCmd(t, "read", "-pcap", pcap, "-ports", "x", "-out", filepath.Join(dir, "x")); err == nil {
		t.Fatal("-ports x accepted")
	}
}

func TestReportDrops(t *testing.T) {
	frames := make(chan flow.Frame)
	sink := newFrameSink(frames)
	sink.put(flow.Frame{})
	var out syncBuf
	reportDrops(&out, sink)
	if !strings.Contains(out.String(), "1 frames were too many for the live view") {
		t.Fatalf("output: %q", out.String())
	}
}
