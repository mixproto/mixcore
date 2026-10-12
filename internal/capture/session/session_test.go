// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package session

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/pcapfile"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var (
	t0      = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	app     = netip.MustParseAddrPort("192.168.2.3:50000")
	console = netip.MustParseAddrPort("192.168.1.62:49280")
)

func TestCreateRefusesThePublicRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/mixproto/mixcore\n\ngo 1.27.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(repo, "research", "captures", "x"), false); !errors.Is(err, ErrPublicRepo) {
		t.Fatalf("err = %v, want ErrPublicRepo", err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Create(filepath.Join(other, "captures", "ui24r", "2026-10-12"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := d.WriteMeta(Meta{Tool: "mixcapture", Mode: "relay", Console: console.String(), Started: t0}); err != nil {
		t.Fatal(err)
	}
	m, err := d.ReadMeta()
	if err != nil || m.Mode != "relay" || m.Privacy != PrivacyNote || !m.Started.Equal(t0) {
		t.Fatalf("meta = %+v, %v", m, err)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func TestIndexAnnotationsRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	iw := NewIndexWriter(nopCloser{&buf})
	o := flow.Observation{At: t0, Dir: flow.ToConsole, App: app, Console: console, Decoder: "text",
		Msg: decode.Message{Key: decode.Key{Kind: decode.KindText, ID: "A/b 0 0"}, Value: decode.NumValue(-1000, "-1000"), Raw: []byte("x")}}
	for _, err := range []error{
		iw.Mark(1, t0, "ch1 fader = -10 dB"),
		iw.Message(o),
		iw.Snapshot(1, t0.Add(time.Second), "before recall"),
		iw.Close(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(buf.String(), `"dir":"app->console"`) ||
		!strings.Contains(buf.String(), `"key":"A/b 0 0","value":"-1000","num":-1000,"raw":"78"`) {
		t.Fatalf("index:\n%s", buf.String())
	}
	ann, err := Annotations(&buf)
	if err != nil || len(ann) != 2 || ann[0].Label != "ch1 fader = -10 dB" || ann[1].Type != TypeSnapshot {
		t.Fatalf("annotations = %+v, %v", ann, err)
	}
}

func TestRecorderFramesDecodeLikeACapture(t *testing.T) {
	var pcap bytes.Buffer
	var frames []flow.Frame
	rec, err := NewRecorder(&pcap, FileCapture, func(f flow.Frame) { frames = append(frames, f) })
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Stream(t0, app, console, []byte("get A/b 0 0\n")); err != nil {
		t.Fatal(err)
	}
	if err := rec.Stream(t0, console, app, []byte("OK get A/b 0 0 7\n")); err != nil {
		t.Fatal(err)
	}
	if err := rec.Datagram(t0, app, netip.MustParseAddrPort("192.168.1.62:10024"), []byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	tab, err := flow.New(flow.Config{Console: console.Addr(), DetectChunks: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	rd, err := pcapfile.NewReader(bytes.NewReader(pcap.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		r, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if r.Offset != frames[i].Evidence.Offset {
			t.Fatalf("frame %d offset %d, pcap offset %d", i, frames[i].Evidence.Offset, r.Offset)
		}
		tab.Add(flow.Frame{At: r.At, LinkType: r.LinkType, Data: r.Data}, func(o flow.Observation) {
			got = append(got, o.Dir.String()+" "+o.Msg.Key.String()+" "+o.Msg.Value.Text)
		})
	}
	want := "app->console A/b 0 0 \nconsole->app A/b 0 0 7\napp->console ping "
	if strings.Join(got, "\n") != want || len(frames) != 5 { // 2 SYN + 3 data packets
		t.Fatalf("observations (%d frames):\n%s\nwant:\n%s", len(frames), strings.Join(got, "\n"), want)
	}
}

// addrConn gives a net.Pipe end distinct TCP addresses, as a real socket has.
type addrConn struct{ net.Conn }

func (addrConn) LocalAddr() net.Addr  { return net.TCPAddrFromAddrPort(app) }
func (addrConn) RemoteAddr() net.Addr { return net.TCPAddrFromAddrPort(console) }

func TestTapConnRecordsBothDirections(t *testing.T) {
	var pcap bytes.Buffer
	var frames []flow.Frame
	rec, err := NewRecorder(&pcap, FileCapture, func(f flow.Frame) { frames = append(frames, f) })
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	tap := TapConn(addrConn{client}, rec)
	done := make(chan []byte, 1)
	go func() {
		b := make([]byte, 16)
		n, _ := server.Read(b)
		_, _ = server.Write([]byte("OK\n"))
		done <- b[:n]
	}()
	if _, err := tap.Write([]byte("prminfo 0\n")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 8)
	n, err := tap.Read(reply)
	if err != nil || string(reply[:n]) != "OK\n" || string(<-done) != "prminfo 0\n" {
		t.Fatalf("read %q, %v", reply[:n], err)
	}
	_ = tap.Close()
	if len(frames) != 4 { // SYN, data, SYN-ACK, data
		t.Fatalf("recorded %d frames, want 4", len(frames))
	}
}

func TestChownKeepsOwnFiles(t *testing.T) {
	d, err := Create(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := d.WriteFile("a.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := d.Chown(os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("Chown to the current user: %v", err)
	}
	if _, err := d.OpenFile("missing"); err == nil {
		t.Fatal("OpenFile of a missing file succeeded")
	}
	if _, err := d.ReadMeta(); err == nil {
		t.Fatal("ReadMeta without session.json succeeded")
	}
}

// Regression (go-reviewer M3): reusing a session directory silently truncated
// the earlier capture. A second Create is refused; overwrite must be explicit.
func TestCreateRefusesAnExistingSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	d, err := Create(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFile(FileCapture, []byte("first capture")); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if _, err := Create(path, false); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("second Create: err = %v, want ErrSessionExists", err)
	}
	d2, err := Create(path, true)
	if err != nil {
		t.Fatalf("Create with overwrite: %v", err)
	}
	_ = d2.Close()
}

// Regression (go-reviewer L1): a symlink outside the repository that points
// into it bypassed the lexical parent walk.
func TestCreateRefusesThePublicRepoThroughASymlink(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/mixproto/mixcore\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "research"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "captures")
	if err := os.Symlink(filepath.Join(repo, "research"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(link, "xr18", "2026-10-12"), false); !errors.Is(err, ErrPublicRepo) {
		t.Fatalf("err = %v, want ErrPublicRepo", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "research", "xr18")); err == nil {
		t.Fatal("a directory was created inside the repository")
	}
}

// Regression (go-reviewer L3): under sudo, parent directories that Create made
// stayed owned by root. Create reports them so Chown can give them back.
func TestCreateReportsTheDirectoriesItMade(t *testing.T) {
	base := t.TempDir()
	d, err := Create(filepath.Join(base, "captures", "xr18", "2026-10-12"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	want := []string{filepath.Join(base, "captures"), filepath.Join(base, "captures", "xr18"),
		filepath.Join(base, "captures", "xr18", "2026-10-12")}
	if got := d.Created(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Created() = %v, want %v", got, want)
	}
	if err := d.Chown(os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
}
