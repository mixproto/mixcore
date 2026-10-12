// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/pcapfile"
	"github.com/mixproto/mixcore/internal/capture/session"
)

type readOptions struct {
	af      analysisFlags
	pcap    string
	console string
	marks   string
	out     string
	force   bool
}

func parseRead(args []string, out io.Writer) (readOptions, error) {
	var o readOptions
	fs := newFlagSet("read", out)
	fs.StringVar(&o.pcap, "pcap", "", "pcap or pcapng file (required)")
	fs.StringVar(&o.console, "console", "", "console IP address (default: guessed from the traffic)")
	fs.StringVar(&o.marks, "marks", "", `marks file: lines "+<seconds> <label>" or "<RFC 3339 time> <label>"; ":s <label>" for a snapshot`)
	fs.StringVar(&o.out, "out", "", "session directory (in the private research repository)")
	fs.BoolVar(&o.force, "force", false, "overwrite a session already in -out")
	o.af.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return o, err
	}
	if o.pcap == "" {
		return o, fmt.Errorf("%w: -pcap is required", errUsage)
	}
	return o, nil
}

func runRead(_ context.Context, args []string, _ io.Reader, out io.Writer) error {
	o, err := parseRead(args, out)
	if err != nil {
		return err
	}
	console, err := readConsole(o, out)
	if err != nil {
		return err
	}
	cfg, err := o.af.flowConfig(console)
	if err != nil {
		return err
	}
	rec, err := startRecording(o.out, o.force, "read", console.String(), map[string]string{"source": filepath.Base(o.pcap)}, out)
	if err != nil {
		return err
	}
	rec.meta.Capture = filepath.Base(o.pcap)
	rec.meta.Ports = cfg.Ports
	e, err := newEngine(&o.af, cfg, rec.idx, out)
	if err != nil {
		return errors.Join(err, rec.idx.Close(), rec.dir.Close())
	}
	runErr := readSession(o, rec, e)
	return errors.Join(runErr, rec.finish(e))
}

func readConsole(o readOptions, out io.Writer) (netip.Addr, error) {
	if o.console != "" {
		return parseConsoleIP(o.console)
	}
	ip, err := guessConsole(o.pcap)
	if err != nil {
		return netip.Addr{}, err
	}
	fmt.Fprintf(out, "console guessed from the traffic: %s (override with -console)\n", ip)
	return ip, nil
}

// readSession copies the file into the session (evidence offsets refer to
// the copy) and replays it with the marks file.
func readSession(o readOptions, rec *recording, e *engine) error {
	if err := copyInto(rec.dir, o.pcap, rec.meta.Capture); err != nil {
		return err
	}
	var ann []session.Record
	if o.marks != "" {
		first, err := firstPacketTime(o.pcap)
		if err != nil {
			return err
		}
		if ann, err = readMarksFile(o.marks, first); err != nil {
			return err
		}
	}
	f, err := rec.dir.OpenFile(rec.meta.Capture)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return replay(e, f, rec.meta.Capture, ann)
}

func copyInto(d *session.Dir, src, name string) error {
	in, err := os.Open(src) //nolint:gosec // G304: the operator names the capture to read
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	defer func() { _ = in.Close() }()
	dst, err := d.Create(name)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(dst, in)
	return errors.Join(cerr, dst.Close())
}

// replay feeds a capture and its annotations (marks, snapshots) in time order.
func replay(e *engine, r io.Reader, file string, ann []session.Record) error {
	rd, err := pcapfile.NewReader(bufio.NewReader(r))
	if err != nil {
		return fmt.Errorf("replay %s: %w", file, err)
	}
	i := 0
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("replay %s: %w", file, err)
		}
		for ; i < len(ann) && !ann[i].T.After(rec.At); i++ {
			apply(e, ann[i])
		}
		e.frame(flow.Frame{At: rec.At, LinkType: rec.LinkType, Data: rec.Data,
			Evidence: flow.Evidence{File: file, Offset: rec.Offset, Packet: rec.Index}})
	}
	for ; i < len(ann); i++ {
		apply(e, ann[i])
	}
	return nil
}

func apply(e *engine, a session.Record) {
	if a.Type == session.TypeSnapshot {
		e.snapshot(a.T, a.Label)
		return
	}
	e.mark(a.T, a.Label)
}

// readMarksFile parses "+<seconds> <label>" (relative to the first packet)
// or "<RFC 3339 time> <label>"; a label ":s <name>" is a snapshot.
func readMarksFile(path string, first time.Time) ([]session.Record, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the operator names the marks file
	if err != nil {
		return nil, fmt.Errorf("marks: %w", err)
	}
	defer func() { _ = f.Close() }()
	var out []session.Record
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		when, label, _ := strings.Cut(line, " ")
		at, err := markTime(when, first)
		if err != nil {
			return nil, fmt.Errorf("%w: marks line %d: %w", errUsage, n, err)
		}
		c := parseCommand(at, label)
		rec := session.Record{Type: session.TypeMark, T: at, Label: c.arg}
		if c.verb == "snap" {
			rec.Type = session.TypeSnapshot
		}
		out = append(out, rec)
	}
	return out, sc.Err()
}

func markTime(s string, first time.Time) (time.Time, error) {
	if rel, ok := strings.CutPrefix(s, "+"); ok {
		sec, err := strconv.ParseFloat(rel, 64)
		if err != nil {
			return time.Time{}, err
		}
		return first.Add(time.Duration(sec * float64(time.Second))), nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

func firstPacketTime(path string) (time.Time, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the operator names the capture to read
	if err != nil {
		return time.Time{}, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }()
	rd, err := pcapfile.NewReader(bufio.NewReader(f))
	if err != nil {
		return time.Time{}, fmt.Errorf("read %s: %w", path, err)
	}
	rec, err := rd.Next()
	if err != nil {
		return time.Time{}, fmt.Errorf("read %s: no packets: %w", path, err)
	}
	return rec.At, nil
}
