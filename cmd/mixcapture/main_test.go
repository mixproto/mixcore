// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/capture/session"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// syncBuf is a terminal the test reads while the command writes.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitFor polls until the output contains text after offset from; it
// returns the index just past the match.
func (s *syncBuf) waitFor(t *testing.T, text string, from int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := s.String()
		if from <= len(out) {
			if i := strings.Index(out[from:], text); i >= 0 {
				return from + i + len(text)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("output never showed %q after offset %d:\n%s", text, from, s.String())
	return 0
}

var (
	t0      = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	app     = netip.MustParseAddrPort("192.168.2.3:51000")
	console = netip.MustParseAddrPort("192.168.1.62:51325")
)

// writePcap writes chunks as a console -> app TCP stream, 100 ms apart, on a
// connection the app opened.
func writePcap(t *testing.T, path string, chunks [][]byte) {
	t.Helper()
	f, err := os.Create(path) //nolint:gosec // G304: a temp path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	rec, err := session.NewRecorder(f, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Stream(t0, app, console, nil); err != nil { // the app opens the connection (SYN)
		t.Fatal(err)
	}
	for i, c := range chunks {
		if err := rec.Stream(t0.Add(time.Duration(i)*100*time.Millisecond), console, app, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func hexFixture(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: a fixture path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		h := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
		if h == "" {
			continue
		}
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a temp path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out syncBuf
	err := runMain(context.Background(), args, strings.NewReader(""), &out)
	return out.String(), err
}

// The MIDI fixture of the decode package as a console's TCP stream, read
// from a pcap with a marks file: the NRPN that changes after the mark is the
// first candidate, with its 7-bit values.
func TestRead_MIDIFixturePcap(t *testing.T) {
	dir := t.TempDir()
	pcap := filepath.Join(dir, "mirror.pcap")
	writePcap(t, pcap, hexFixture(t, "../../internal/capture/decode/testdata/midi-nrpn.hex"))
	writeFile(t, filepath.Join(dir, "marks.txt"), "# offsets from the first packet\n+0.25 ch1 param\n")
	out := filepath.Join(dir, "session")
	got, err := runCmd(t, "read", "-pcap", pcap, "-marks", filepath.Join(dir, "marks.txt"), "-out", out)
	if err != nil {
		t.Fatalf("read: %v\n%s", err, got)
	}
	if !strings.Contains(got, "console guessed from the traffic: 192.168.1.62") || !strings.Contains(got, "PRIVACY:") {
		t.Fatalf("output:\n%s", got)
	}
	marks := readFile(t, filepath.Join(out, "marks.csv"))
	if !strings.Contains(marks, "1,ch1 param,NRPN ch1 0x0017,0x5A,0x5F,console->app,1,false,mirror.pcap:") {
		t.Fatalf("marks.csv:\n%s", marks)
	}
	if md := readFile(t, filepath.Join(out, "report.md")); !strings.Contains(md, "tcp 192.168.2.3:51000 <-> 192.168.1.62:51325: midi") {
		t.Fatalf("report.md flows:\n%s", md)
	}
	if readFile(t, filepath.Join(out, "mirror.pcap")) != readFile(t, pcap) {
		t.Fatal("the session copy of the capture differs from the source")
	}
}

// The JSON-lines fixture as a console's TCP stream: the keyed object is
// attributed by its path, and a later report run gives the same marks.
func TestRead_JSONFixturePcapAndReportRerun(t *testing.T) {
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
	writeFile(t, filepath.Join(dir, "marks.txt"), "+0.25 ch1 volume up\n+0.05 :s first\n+0.35 :s second\n")
	out := filepath.Join(dir, "session")
	got, err := runCmd(t, "read", "-pcap", pcap, "-console", "192.168.1.62", "-marks", filepath.Join(dir, "marks.txt"), "-out", out)
	if err != nil {
		t.Fatalf("read: %v\n%s", err, got)
	}
	marks := readFile(t, filepath.Join(out, "marks.csv"))
	if !strings.Contains(marks, "1,ch1 volume up,line/ch1/volume,0.5,0.75,console->app,1,false") {
		t.Fatalf("marks.csv:\n%s", marks)
	}
	if !strings.Contains(got, "diff snapshot 1 -> 2: ") {
		t.Fatalf("no snapshot diff printed:\n%s", got)
	}
	if got, err := runCmd(t, "report", "-session", out); err != nil || !strings.Contains(got, "report: ") {
		t.Fatalf("report: %v\n%s", err, got)
	}
	if again := readFile(t, filepath.Join(out, "marks.csv")); again != marks {
		t.Fatalf("report re-run changed marks.csv:\n%s\nwas:\n%s", again, marks)
	}
}

// Live mode with a stand-in tcpdump that prints a pcap: the stream is copied
// byte for byte into capture.pcap and analyzed.
func TestLive_TeesTcpdumpOutput(t *testing.T) {
	dir := t.TempDir()
	pcap := filepath.Join(dir, "fixture.pcap")
	writePcap(t, pcap, hexFixture(t, "../../internal/capture/decode/testdata/midi-nrpn.hex"))
	fake := filepath.Join(dir, "tcpdump")
	writeFile(t, fake, "#!/bin/sh\ncat '"+pcap+"'\n")
	if err := os.Chmod(fake, 0o700); err != nil { //nolint:gosec // G302: the test's own stand-in executable
		t.Fatal(err)
	}
	out := filepath.Join(dir, "session")
	got, err := runCmd(t, "live", "-i", "bridge100", "-console", "192.168.1.62", "-tcpdump", fake, "-out", out)
	if err != nil {
		t.Fatalf("live: %v\n%s", err, got)
	}
	if readFile(t, filepath.Join(out, session.FileCapture)) != readFile(t, pcap) {
		t.Fatal("capture.pcap is not a byte copy of the tcpdump output")
	}
	if keys := readFile(t, filepath.Join(out, "keys.csv")); !strings.Contains(keys, "NRPN ch1 0x0017,nrpn,midi") {
		t.Fatalf("keys.csv:\n%s", keys)
	}
	if idx := readFile(t, filepath.Join(out, session.FileIndex)); !strings.Contains(idx, `"key":"ch1 0x0017","value":"0x5A"`) {
		t.Fatalf("index.jsonl:\n%s", idx)
	}
}

func TestLive_TcpdumpFailureExplainsPermissions(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tcpdump")
	writeFile(t, fake, "#!/bin/sh\necho 'tcpdump: bridge100: You don'\\''t have permission to capture on that device' >&2\nexit 1\n")
	if err := os.Chmod(fake, 0o700); err != nil { //nolint:gosec // G302: the test's own stand-in executable
		t.Fatal(err)
	}
	got, err := runCmd(t, "live", "-i", "bridge100", "-console", "192.168.1.62", "-tcpdump", fake, "-out", filepath.Join(dir, "s"))
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("err = %v\n%s", err, got)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	tests := [][]string{
		{},
		{"nope"},
		{"relay", "-console", "192.168.1.62", "-udp", "10024", "-out", dir},         // no -listen
		{"relay", "-console", "192.168.1.62", "-listen", "127.0.0.1", "-out", dir},  // no ports
		{"relay", "-console", "console.local", "-listen", "127.0.0.1", "-udp", "1"}, // not an IP
		{"live", "-console", "192.168.1.62", "-out", dir},                           // no -i
		{"read", "-out", dir}, // no -pcap
		{"introspect", "-family", "x32", "-console", "192.168.1.62", "-out", dir}, // no self-description
		{"relay", "-console", "192.168.1.62", "-listen", "127.0.0.1", "-udp", "1", "-decoder", "1=nope", "-out", dir},
		{"law", "-p", "0.5"},
	}
	for _, args := range tests {
		if _, err := runCmd(t, args...); !errors.Is(err, errUsage) && !strings.Contains(errString(err), "decoder") &&
			!strings.Contains(errString(err), "law") {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
	if _, err := runCmd(t, "relay", "-console", "192.168.1.62", "-listen", "127.0.0.1", "-udp", "1"); !errors.Is(err, errUsage) {
		t.Errorf("relay without -out: err = %v", err)
	}
	if _, err := runCmd(t, "introspect", "-family", "yamaha-rcp", "-console", "192.168.1.62"); !errors.Is(err, errUsage) {
		t.Errorf("introspect without -out: err = %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestLawFromPointsOnly(t *testing.T) {
	got, err := runCmd(t, "law", "-p", "0.0625=-60", "-p", "0.25=-30", "-p", "0.5=-10", "-p", "1=10", "-p", "0=-inf")
	if err != nil {
		t.Fatalf("law: %v\n%s", err, got)
	}
	if !strings.Contains(got, "best: piecewise linear, 3 segment(s): [0.0625..0.25] y = 160*x - 70;") ||
		!strings.Contains(got, "x <= 0 is -inf") {
		t.Fatalf("law output:\n%s", got)
	}
}

func TestParseCommand(t *testing.T) {
	for line, want := range map[string]string{
		"ch1 fader up": "mark:ch1 fader up", "": "mark:", ":s after recall": "snap:after recall",
		":keys fader": "keys:fader", ":law /ch/01/mix/fader": "law:/ch/01/mix/fader", ":q": "quit:", ":zzz": "help:",
	} {
		c := parseCommand(t0, line)
		if got := c.verb + ":" + c.arg; got != want {
			t.Errorf("parseCommand(%q) = %q, want %q", line, got, want)
		}
	}
}

// Regression (go-reviewer M2): the relay must not listen on every interface or
// on an address this machine does not have, and -app must be IP addresses.
func TestRelayListenMustBeOneLocalAddress(t *testing.T) {
	dir := t.TempDir()
	for _, listen := range []string{"0.0.0.0", "::", "192.0.2.1"} {
		_, err := runCmd(t, "relay", "-console", "192.168.1.62", "-listen", listen, "-udp", "10024", "-out", dir)
		if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "-listen") {
			t.Errorf("-listen %s: err = %v, want a usage error", listen, err)
		}
	}
	_, err := runCmd(t, "relay", "-console", "192.168.1.62", "-listen", "127.0.0.1", "-udp", "10024", "-app", "tablet", "-out", dir)
	if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "-app") {
		t.Errorf("-app tablet: err = %v", err)
	}
}

// Regression (go-reviewer M3): a second recording into the same -out is
// refused unless -force, so an earlier capture is never truncated silently.
func TestReadRefusesAnExistingSessionWithoutForce(t *testing.T) {
	dir := t.TempDir()
	pcap := filepath.Join(dir, "mirror.pcap")
	writePcap(t, pcap, hexFixture(t, "../../internal/capture/decode/testdata/midi-nrpn.hex"))
	out := filepath.Join(dir, "session")
	if got, err := runCmd(t, "read", "-pcap", pcap, "-out", out); err != nil {
		t.Fatalf("first read: %v\n%s", err, got)
	}
	if _, err := runCmd(t, "read", "-pcap", pcap, "-out", out); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("second read into the same session: err = %v, want a refusal naming -force", err)
	}
	if got, err := runCmd(t, "read", "-pcap", pcap, "-out", out, "-force"); err != nil {
		t.Fatalf("read -force: %v\n%s", err, got)
	}
}

// Regression (go-reviewer L3): the filter is passed after "--", so a filter
// that starts with '-' can never be read by tcpdump as an option (e.g. -w).
func TestLive_FilterAfterDoubleDash(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "tcpdump")
	writeFile(t, fake, "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done > '"+argsFile+"'\n")
	if err := os.Chmod(fake, 0o700); err != nil { //nolint:gosec // G302: the test's own stand-in executable
		t.Fatal(err)
	}
	_, _ = runCmd(t, "live", "-i", "bridge100", "-console", "192.168.1.62", "-tcpdump", fake,
		"-filter", "-w /tmp/evil", "-out", filepath.Join(dir, "s"))
	args := strings.Split(strings.TrimSpace(readFile(t, argsFile)), "\n")
	if n := len(args); n < 2 || args[n-2] != "--" || args[n-1] != "-w /tmp/evil" {
		t.Fatalf("tcpdump argv = %q, want the filter last, after --", args)
	}
}
