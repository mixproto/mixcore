// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package rcpinfo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeConsole answers RCP lines from a table shaped like YR-F05 and records
// every line it received. It sends a NOTIFY before some replies (YR-F03).
type fakeConsole struct {
	mu       sync.Mutex
	received []string
	prm      []string // reply bodies after "OK prminfo <i> "
	silentAt int      // -1: always answer; else stay silent from this index (T1 timeout variant)
}

func (f *fakeConsole) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := sc.Text()
		f.mu.Lock()
		f.received = append(f.received, line)
		f.mu.Unlock()
		if reply := f.reply(line); reply != "" {
			if _, err := fmt.Fprint(conn, reply); err != nil {
				return
			}
		}
	}
}

func (f *fakeConsole) reply(line string) string {
	verb, arg, _ := strings.Cut(line, " ")
	switch verb {
	case VerbDevInfo:
		return fmt.Sprintf("NOTIFY set MIXER:Current/InCh/Fader/Level 0 0 -500\nOK devinfo %s \"TF5\"\n", arg)
	case VerbPrmInfo:
		var i int
		if _, err := fmt.Sscanf(arg, "%d", &i); err != nil {
			return "ERROR prminfo InvalidArgument\n"
		}
		if f.silentAt >= 0 && i >= f.silentAt {
			return ""
		}
		if i >= len(f.prm) {
			return "ERROR prminfo InvalidArgument\n"
		}
		return fmt.Sprintf("OK prminfo %d %s\n", i, f.prm[i])
	default:
		return "ERROR " + verb + " UnknownCommand\n"
	}
}

func start(t *testing.T, f *fakeConsole, timeout time.Duration) *Client {
	t.Helper()
	client, server := net.Pipe()
	var wg sync.WaitGroup
	wg.Go(func() { f.serve(server) })
	t.Cleanup(func() { _ = client.Close(); wg.Wait() })
	return NewClient(client, timeout)
}

var sampleTable = []string{
	`"MIXER:Current/InCh/Fader/Level" 40 1 -32768 1000 -32768 "dB" integer any rw 100`,
	`"MIXER:Current/InCh/Fader/On" 40 1 0 1 1 "" integer any rw 1`,
}

func TestTableUntilError(t *testing.T) {
	f := &fakeConsole{prm: sampleTable, silentAt: -1}
	c := start(t, f, time.Second)
	name, err := c.DevInfo("productname")
	if err != nil || name != "TF5" || c.Skipped != 1 {
		t.Fatalf("DevInfo = %q, %v (skipped %d)", name, err, c.Skipped)
	}
	var progress []int
	entries, why, err := c.Table(context.Background(), VerbPrmInfo, DefaultMaxIndex, func(n int) { progress = append(progress, n) })
	if err != nil || len(entries) != 2 || !strings.Contains(why, "ERROR at index 2") || len(progress) != 2 {
		t.Fatalf("Table = %d entries, %q, %v", len(entries), why, err)
	}
	e := entries[0]
	if e.Address != "MIXER:Current/InCh/Fader/Level" || e.Field("min") != "-32768" || e.Field("unit") != "dB" ||
		e.Field("scale") != "100" || e.Field("nope") != "" {
		t.Fatalf("entry = %+v", e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, line := range f.received {
		if err := CheckReadOnly(line); err != nil {
			t.Fatalf("the console received a non read-only line: %v", err)
		}
	}
}

func TestTableStopsOnSilenceAndCap(t *testing.T) {
	f := &fakeConsole{prm: sampleTable, silentAt: 1}
	c := start(t, f, 50*time.Millisecond)
	entries, why, err := c.Table(context.Background(), VerbPrmInfo, DefaultMaxIndex, nil)
	if err != nil || len(entries) != 1 || !strings.Contains(why, "no reply at index 1") {
		t.Fatalf("Table = %d entries, %q, %v", len(entries), why, err)
	}
	c2 := start(t, &fakeConsole{prm: sampleTable, silentAt: -1}, time.Second)
	if entries, why, _ := c2.Table(context.Background(), VerbPrmInfo, 1, nil); len(entries) != 1 || !strings.Contains(why, "cap 1") {
		t.Fatalf("cap: %d entries, %q", len(entries), why)
	}
}

func TestCheckReadOnly(t *testing.T) {
	for _, ok := range []string{"devinfo productname", "prminfo 0", "mtrinfo 123"} {
		if err := CheckReadOnly(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"set MIXER:Current/InCh/Fader/Level 0 0 -1000",
		"ssrecall_ex scene_a 5", "scpmode keepalive 1000", "mtrstart MIXER:Current/InCh/PreFader 100",
		"prminfo 0\nset x 0 0 1", "prminfo", "prminfo -1", "prminfo 0 0", "PRMINFO 0", "devinfo ../x",
	} {
		if err := CheckReadOnly(bad); !errors.Is(err, ErrNotReadOnly) {
			t.Errorf("%q accepted", bad)
		}
	}
	c := NewClient(nil, 0) // a refused line never reaches the (nil) connection
	if _, _, err := c.Query("set MIXER:Current/InCh/Fader/Level 0 0 0"); !errors.Is(err, ErrNotReadOnly) {
		t.Fatalf("Query err = %v", err)
	}
	if _, _, err := c.Table(context.Background(), "set", 1, nil); !errors.Is(err, ErrNotReadOnly) {
		t.Fatalf("Table err = %v", err)
	}
}

// FuzzReplies: whatever a console sends back, the client never panics and
// only ever writes read-only lines.
func FuzzReplies(f *testing.F) {
	f.Add("OK prminfo 0 \"A\" 1 2\nERROR prminfo x\n")
	f.Add("NOTIFY x\nOK prminfo 0\nOK prminfo 1 \"unterminated\n")
	f.Fuzz(func(t *testing.T, replies string) {
		client, server := net.Pipe()
		var wg sync.WaitGroup
		var sent []string
		wg.Go(func() { _, _ = fmt.Fprint(server, replies) })
		wg.Go(func() {
			defer func() { _ = server.Close() }()
			sc := bufio.NewScanner(server)
			for sc.Scan() {
				sent = append(sent, sc.Text())
			}
		})
		c := NewClient(client, 20*time.Millisecond)
		_, _, _ = c.Table(context.Background(), VerbPrmInfo, 3, nil)
		_ = client.Close()
		wg.Wait()
		for _, s := range sent {
			if CheckReadOnly(s) != nil {
				t.Fatalf("sent %q", s)
			}
		}
	})
}

// Regression (go-reviewer L8): the public protocol document this package
// implements from must cite its public sources (YS1-YS3) only, never a path
// in the private product repository.
func TestProtocolDocCitesPublicSourcesOnly(t *testing.T) {
	b, err := os.ReadFile("../../../docs/protocols/yamaha-rcp.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, private := range []string{"docs/research/", "dybyskz", "Mixhand", "private research"} {
		if strings.Contains(doc, private) {
			t.Errorf("docs/protocols/yamaha-rcp.md mentions %q", private)
		}
	}
	for _, src := range []string{"**YS1**", "**YS2**", "**YS3**"} {
		if !strings.Contains(doc, src) {
			t.Errorf("source %s missing", src)
		}
	}
}
