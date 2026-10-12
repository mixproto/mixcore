// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeRec records what the relay reports.
type fakeRec struct {
	mu   sync.Mutex
	logs []string
}

func (r *fakeRec) add(kind string, src, dst netip.AddrPort, b []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf("%s %s>%s %q", kind, src, dst, b))
	return nil
}

func (r *fakeRec) Datagram(_ time.Time, src, dst netip.AddrPort, b []byte) error {
	return r.add("udp", src, dst, b)
}

func (r *fakeRec) Stream(_ time.Time, src, dst netip.AddrPort, b []byte) error {
	return r.add("tcp", src, dst, b)
}

func (r *fakeRec) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.logs...)
}

const (
	consoleAddr = "10.0.0.1:10024"
	relayIP     = "10.0.0.9"
	appIP       = "10.0.0.2"
)

type udpRig struct {
	net   *memnet.Network
	sim   *simxair.Sim
	rec   *fakeRec
	relay *net.UDPAddr
}

// startUDP runs an X Air simulator and the relay on memnet until the test ends.
func startUDP(t *testing.T, idle time.Duration) *udpRig {
	t.Helper()
	n := memnet.New()
	simPC, err := n.ListenPacket(consoleAddr)
	if err != nil {
		t.Fatal(err)
	}
	listen, err := n.ListenPacket(relayIP + ":10024")
	if err != nil {
		t.Fatal(err)
	}
	r := &udpRig{net: n, sim: simxair.New(simxair.Options{}), rec: &fakeRec{}, relay: net.UDPAddrFromAddrPort(netip.MustParseAddrPort(relayIP + ":10024"))}
	u := &UDP{Listen: listen, Console: netip.MustParseAddrPort(consoleAddr), Rec: r.rec, Idle: idle,
		Upstream: func(context.Context) (net.PacketConn, error) { return n.ListenPacket(relayIP + ":0") }}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = r.sim.Serve(ctx, simPC) })
	wg.Go(func() {
		if err := u.Serve(ctx); !errors.Is(err, ErrClosed) {
			t.Errorf("Serve = %v, want ErrClosed", err)
		}
	})
	t.Cleanup(func() { cancel(); wg.Wait(); _ = listen.Close(); _ = simPC.Close() })
	return r
}

func (r *udpRig) app(t *testing.T) *memnet.PacketConn {
	t.Helper()
	pc, err := r.net.ListenPacket(appIP + ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

func send(t *testing.T, pc net.PacketConn, to net.Addr, addr string, args ...osc.Arg) {
	t.Helper()
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.WriteTo(b, to); err != nil {
		t.Fatal(err)
	}
}

func recvAddr(t *testing.T, pc net.PacketConn) (string, net.Addr) {
	t.Helper()
	buf := make([]byte, 2048)
	_ = pc.SetReadDeadline(time.Now().Add(time.Second))
	n, from, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("no reply through the relay: %v", err)
	}
	var m osc.Message
	if err := osc.Decode(buf[:n], &m); err != nil {
		t.Fatal(err)
	}
	return string(m.Address), from
}

func TestUDPRelayForwardsBothWaysAndOnlyWhatTheAppSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startUDP(t, time.Minute)
		time.Sleep(time.Hour) // an idle relay sends nothing to the console
		synctest.Wait()
		if n := len(r.sim.Log()); n != 0 {
			t.Fatalf("console received %d packets with no app traffic", n)
		}
		app := r.app(t)
		send(t, app, r.relay, "/xinfo")
		if addr, from := recvAddr(t, app); addr != "/xinfo" || from.String() != r.relay.String() {
			t.Fatalf("reply %s from %s", addr, from)
		}
		send(t, app, r.relay, "/ch/01/mix/fader", osc.Float(0.5))
		synctest.Wait()
		log, writes := r.sim.Log(), r.sim.Writes()
		if len(log) != 2 || len(writes) != 1 || writes[0].Address != "/ch/01/mix/fader" {
			t.Fatalf("console log = %+v", log)
		}
		if !strings.HasPrefix(log[0].From.String(), relayIP+":") {
			t.Fatalf("console saw %s, want the relay's upstream socket", log[0].From)
		}
		got := r.rec.all()
		if len(got) != 3 || !strings.HasPrefix(got[0], "udp "+appIP) || !strings.Contains(got[1], consoleAddr+">"+appIP) {
			t.Fatalf("recorded:\n%s", strings.Join(got, "\n"))
		}
	})
}

func TestUDPRelayDropsStrangersAndReapsIdleApps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startUDP(t, 10*time.Second)
		app := r.app(t)
		send(t, app, r.relay, "/xinfo")
		recvAddr(t, app)
		upstream := r.sim.Log()[0].From
		stranger, err := r.net.ListenPacket("10.0.0.66:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stranger.Close() }()
		send(t, stranger, net.UDPAddrFromAddrPort(upstream), "/ch/01/mix/fader", osc.Float(1))
		synctest.Wait()
		_ = app.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := app.ReadFrom(make([]byte, 64)); err == nil {
			t.Fatal("a non-console packet reached the app")
		}
		time.Sleep(time.Minute) // idle: the upstream socket is closed
		send(t, app, r.relay, "/xinfo")
		recvAddr(t, app)
		if log := r.sim.Log(); log[len(log)-1].From == upstream {
			t.Fatal("idle app kept its upstream socket")
		}
	})
}

// pipeListener hands out the server ends of net.Pipe pairs.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

func (l *pipeListener) dial() net.Conn {
	return l.dialFrom(netip.MustParseAddrPort(appIP + ":40000"))
}

// dialFrom connects with the given remote address on the server end.
func (l *pipeListener) dialFrom(from netip.AddrPort) net.Conn {
	client, server := net.Pipe()
	l.conns <- remoteConn{Conn: server, remote: net.TCPAddrFromAddrPort(from)}
	return client
}

type remoteConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteConn) RemoteAddr() net.Addr { return c.remote }

func TestTCPRelayPipesAndRecords(t *testing.T) {
	ln := newPipeListener()
	rec := &fakeRec{}
	var wg sync.WaitGroup
	console := netip.MustParseAddrPort("10.0.0.1:49280")
	tr := &TCP{Listener: ln, Console: console, Rec: rec, Dial: func(context.Context) (net.Conn, error) {
		c, s := net.Pipe()
		wg.Go(func() { // the console answers each line with OK
			defer func() { _ = s.Close() }()
			sc := bufio.NewScanner(s)
			for sc.Scan() {
				if _, err := fmt.Fprintf(s, "OK %s\n", sc.Text()); err != nil {
					return
				}
			}
		})
		return c, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	wg.Go(func() { _ = tr.Serve(ctx) })
	app := ln.dial()
	if _, err := fmt.Fprint(app, "devinfo productname\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(app).ReadString('\n')
	if err != nil || line != "OK devinfo productname\n" {
		t.Fatalf("reply %q, %v", line, err)
	}
	_ = app.Close()
	cancel()
	wg.Wait()
	got := rec.all()
	if len(got) != 2 || !strings.Contains(got[0], ">10.0.0.1:49280 \"devinfo productname\\n\"") ||
		!strings.HasPrefix(got[1], "tcp 10.0.0.1:49280>") {
		t.Fatalf("recorded:\n%s", strings.Join(got, "\n"))
	}
}

// Regression (go-reviewer M2): with an allow-list, datagrams from any other
// source are dropped before an upstream socket exists, so nothing reaches
// the console.
func TestUDPRelayDropsSourcesOutsideTheAllowList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		simPC, err := n.ListenPacket(consoleAddr)
		if err != nil {
			t.Fatal(err)
		}
		listen, err := n.ListenPacket(relayIP + ":10024")
		if err != nil {
			t.Fatal(err)
		}
		sim := simxair.New(simxair.Options{})
		u := &UDP{Listen: listen, Console: netip.MustParseAddrPort(consoleAddr), Rec: &fakeRec{},
			Apps:     []netip.Addr{netip.MustParseAddr(appIP)},
			Upstream: func(context.Context) (net.PacketConn, error) { return n.ListenPacket(relayIP + ":0") }}
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { _ = sim.Serve(ctx, simPC) })
		wg.Go(func() { _ = u.Serve(ctx) })
		defer func() { cancel(); wg.Wait(); _ = listen.Close(); _ = simPC.Close() }()
		relay := net.UDPAddrFromAddrPort(netip.MustParseAddrPort(relayIP + ":10024"))
		stranger, err := n.ListenPacket("10.0.0.66:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stranger.Close() }()
		send(t, stranger, relay, "/ch/01/mix/fader", osc.Float(1))
		synctest.Wait()
		if log := sim.Log(); len(log) != 0 {
			t.Fatalf("a source outside the allow-list reached the console: %+v", log)
		}
		app, err := n.ListenPacket(appIP + ":0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = app.Close() }()
		send(t, app, relay, "/xinfo")
		if addr, _ := recvAddr(t, app); addr != "/xinfo" {
			t.Fatalf("allowed app got %s", addr)
		}
	})
}

// Regression (go-reviewer M2): the TCP relay closes a connection from a
// source outside the allow-list without dialing the console.
func TestTCPRelayRejectsSourcesOutsideTheAllowList(t *testing.T) {
	ln := newPipeListener()
	var dials int
	var mu sync.Mutex
	tr := &TCP{Listener: ln, Console: netip.MustParseAddrPort("10.0.0.1:49280"), Rec: &fakeRec{},
		Apps: []netip.Addr{netip.MustParseAddr(appIP)},
		Dial: func(context.Context) (net.Conn, error) {
			mu.Lock()
			dials++
			mu.Unlock()
			return nil, errors.New("must not dial")
		}}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = tr.Serve(ctx) })
	c := ln.dialFrom(netip.MustParseAddrPort("10.0.0.66:40000"))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection from a stranger stayed open")
	}
	cancel()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if dials != 0 {
		t.Fatalf("relay dialed the console %d times for a stranger", dials)
	}
}

// flakyListener fails Accept with the queued errors first, and counts Close.
type flakyListener struct {
	*pipeListener
	mu     sync.Mutex
	errs   []error
	closes int
}

func (l *flakyListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		l.mu.Unlock()
		return nil, err
	}
	l.mu.Unlock()
	return l.pipeListener.Accept()
}

func (l *flakyListener) Close() error {
	l.mu.Lock()
	l.closes++
	l.mu.Unlock()
	return l.pipeListener.Close()
}

// Regression (go-reviewer L6): one EMFILE from Accept ended the TCP relay.
// A transient accept error is retried; the next app connection is served.
func TestTCPRelaySurvivesATransientAcceptError(t *testing.T) {
	ln := &flakyListener{pipeListener: newPipeListener(), errs: []error{&net.OpError{Op: "accept", Err: syscall.EMFILE}}}
	console := netip.MustParseAddrPort("10.0.0.1:49280")
	var wg sync.WaitGroup
	tr := &TCP{Listener: ln, Console: console, Rec: &fakeRec{}, Dial: func(context.Context) (net.Conn, error) {
		c, s := net.Pipe()
		wg.Go(func() { defer func() { _ = s.Close() }(); _, _ = io.Copy(s, s) }) // an echoing console
		return c, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	wg.Go(func() { served <- tr.Serve(ctx) })
	client, server := net.Pipe()
	select {
	case ln.conns <- server:
	case err := <-served:
		cancel()
		wg.Wait()
		t.Fatalf("relay stopped after one EMFILE: Serve returned %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not accept after a transient error")
	}
	app := client
	if _, err := app.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(app, b); err != nil || b[0] != 'x' {
		t.Fatalf("echo through the relay: %q, %v", b, err)
	}
	_ = app.Close()
	cancel()
	wg.Wait()
}

// Regression (go-reviewer L6): Serve must close its listener on every
// return, also when Accept fails for good without a cancelled context.
func TestTCPRelayClosesItsListenerOnEveryReturn(t *testing.T) {
	ln := &flakyListener{pipeListener: newPipeListener(), errs: []error{net.ErrClosed}}
	tr := &TCP{Listener: ln, Console: netip.MustParseAddrPort("10.0.0.1:49280"), Rec: &fakeRec{},
		Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("unused") }}
	if err := tr.Serve(context.Background()); err == nil {
		t.Fatal("Serve returned nil after the listener failed")
	}
	ln.mu.Lock()
	defer ln.mu.Unlock()
	if ln.closes == 0 {
		t.Fatal("Serve returned without closing its listener")
	}
}
