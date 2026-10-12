// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/internal/capture/rcpinfo"
)

// rcpConsole is a scripted TCP text console for tests: it answers devinfo
// and prminfo from a two-entry table (shapes of docs/protocols/yamaha-rcp.md
// YR-F04, YR-F05), echoes set as OK set plus a NOTIFY to the other clients,
// and logs every line it receives.
type rcpConsole struct {
	ln    net.Listener
	mu    sync.Mutex
	lines []string
	conns []net.Conn
	wg    sync.WaitGroup
}

var rcpTable = []string{
	`"MIXER:Current/InCh/Fader/Level" 40 1 -32768 1000 -32768 "dB" integer any rw 100`,
	`"MIXER:Current/InCh/Fader/On" 40 1 0 1 1 "" integer any rw 1`,
}

func startRCPConsole(t *testing.T) *rcpConsole {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &rcpConsole{ln: ln}
	c.wg.Go(c.accept)
	t.Cleanup(func() {
		_ = ln.Close()
		c.mu.Lock()
		for _, conn := range c.conns {
			_ = conn.Close()
		}
		c.mu.Unlock()
		c.wg.Wait()
	})
	return c
}

func (c *rcpConsole) port() int { return c.ln.Addr().(*net.TCPAddr).Port }

func (c *rcpConsole) accept() {
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			return
		}
		c.mu.Lock()
		c.conns = append(c.conns, conn)
		c.mu.Unlock()
		c.wg.Go(func() { c.serve(conn) })
	}
}

func (c *rcpConsole) serve(conn net.Conn) {
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := sc.Text()
		c.mu.Lock()
		c.lines = append(c.lines, line)
		c.mu.Unlock()
		if _, err := io.WriteString(conn, c.reply(line)); err != nil {
			return
		}
	}
}

func (c *rcpConsole) reply(line string) string {
	verb, rest, _ := strings.Cut(line, " ")
	switch verb {
	case "devinfo":
		return fmt.Sprintf("OK devinfo %s \"TF5\"\n", rest)
	case "prminfo":
		i, err := strconv.Atoi(rest)
		if err != nil || i >= len(rcpTable) {
			return "ERROR prminfo InvalidArgument\n"
		}
		return fmt.Sprintf("OK prminfo %d %s\n", i, rcpTable[i])
	case "mtrinfo":
		return "ERROR mtrinfo InvalidArgument\n"
	case "set", "get":
		return "OK " + line + "\n"
	default:
		return "ERROR " + verb + " UnknownCommand\n"
	}
}

func (c *rcpConsole) received() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func TestE2E_IntrospectIsReadOnly(t *testing.T) {
	con := startRCPConsole(t)
	dir := filepath.Join(t.TempDir(), "tf5")
	got, err := runCmd(t, "introspect", "-family", "yamaha-rcp", "-console", "127.0.0.1",
		"-port", strconv.Itoa(con.port()), "-out", dir)
	if err != nil {
		t.Fatalf("introspect: %v\n%s", err, got)
	}
	for _, line := range con.received() {
		if err := rcpinfo.CheckReadOnly(line); err != nil {
			t.Fatalf("the console received %q", line)
		}
	}
	if !strings.Contains(got, "prminfo: 2 entries, stopped: console answered ERROR at index 2") {
		t.Fatalf("output:\n%s", got)
	}
	params := readFile(t, filepath.Join(dir, "params.csv"))
	if !strings.Contains(params, "1,MIXER:Current/InCh/Fader/On,40,1,0,1,1,,integer,any,rw,1,") {
		t.Fatalf("params.csv:\n%s", params)
	}
	// The conversation is evidence: the recorded pcap decodes as text keys.
	sess := filepath.Join(t.TempDir(), "re-read")
	got, err = runCmd(t, "read", "-pcap", filepath.Join(dir, "capture.pcap"), "-console", "127.0.0.1",
		"-ports", strconv.Itoa(con.port()), "-out", sess)
	if err != nil || !strings.Contains(readFile(t, filepath.Join(sess, "keys.csv")), "devinfo productname") {
		t.Fatalf("read of the introspection capture: %v\n%s", err, got)
	}
}

var tcpRelayLine = regexp.MustCompile(`relay tcp (127\.0\.0\.1:\d+) -> `)

// The TCP relay with a scripted text app: a mark attributes the parameter the
// app sets, and the console receives only the app's own lines.
func TestE2E_TCPRelayTextMarks(t *testing.T) {
	con := startRCPConsole(t)
	out := &syncBuf{}
	stdinR, stdinW := io.Pipe()
	dir := filepath.Join(t.TempDir(), "tf5-relay")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		done <- runMain(ctx, []string{"relay", "-console", "127.0.0.1", "-listen", "127.0.0.1",
			"-tcp", fmt.Sprintf("0:%d", con.port()), "-out", dir, "-window", "1s"}, stdinR, out)
	})
	t.Cleanup(func() { cancel(); _ = stdinW.Close(); wg.Wait() })
	pos := out.waitFor(t, "recording;", 0, outputWait)
	m := tcpRelayLine.FindStringSubmatch(out.String())
	app, err := net.Dial("tcp4", m[1])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	r := bufio.NewReader(app)
	say := func(line string) {
		if _, err := io.WriteString(app, line+"\n"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	say("get MIXER:Current/InCh/Fader/Level 0 0")
	say("set MIXER:Current/InCh/Fader/Level 0 0 -32768")
	time.Sleep(300 * time.Millisecond)
	if _, err := io.WriteString(stdinW, "ch1 fader = -10 dB\n"); err != nil {
		t.Fatal(err)
	}
	pos = out.waitFor(t, `mark 1 "ch1 fader = -10 dB"`, pos, outputWait)
	say("set MIXER:Current/InCh/Fader/Level 0 0 -1000")
	out.waitFor(t, "1. MIXER:Current/InCh/Fader/Level 0 0 -32768→-1000", pos, outputWait)
	if _, err := io.WriteString(stdinW, ":q\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("relay: %v\n%s", err, out.String())
	}
	want := []string{"get MIXER:Current/InCh/Fader/Level 0 0", "set MIXER:Current/InCh/Fader/Level 0 0 -32768",
		"set MIXER:Current/InCh/Fader/Level 0 0 -1000"}
	if got := con.received(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("console received:\n%s\nwant exactly the app's lines:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
