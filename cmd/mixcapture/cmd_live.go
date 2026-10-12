// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os/exec"
	"strings"
	"sync"

	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/pcapfile"
	"github.com/mixproto/mixcore/internal/capture/session"
	"github.com/mixproto/mixcore/internal/safe"
)

const defaultTcpdump = "tcpdump"

type liveOptions struct {
	af      analysisFlags
	console netip.Addr
	iface   string
	tcpdump string
	filter  string
	out     string
	force   bool
}

func parseLive(args []string, out io.Writer) (liveOptions, error) {
	var o liveOptions
	fs := newFlagSet("live", out)
	consoleText := fs.String("console", "", "console IP address (required)")
	fs.StringVar(&o.iface, "i", "", "interface to capture on, e.g. bridge100 (Internet Sharing) or en0 (required)")
	fs.StringVar(&o.tcpdump, "tcpdump", defaultTcpdump, "tcpdump binary")
	fs.StringVar(&o.filter, "filter", "", `capture filter (default "host <console>")`)
	fs.StringVar(&o.out, "out", "", "session directory (in the private research repository)")
	fs.BoolVar(&o.force, "force", false, "overwrite a session already in -out")
	o.af.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return o, err
	}
	var err error
	if o.console, err = parseConsoleIP(*consoleText); err != nil {
		return o, err
	}
	if o.iface == "" || strings.HasPrefix(o.iface, "-") {
		return o, fmt.Errorf("%w: -i <interface> is required (ifconfig lists them; Internet Sharing makes bridge100)", errUsage)
	}
	if o.filter == "" {
		o.filter = "host " + o.console.String()
	}
	return o, nil
}

func runLive(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	o, err := parseLive(args, out)
	if err != nil {
		return err
	}
	cfg, err := o.af.flowConfig(o.console)
	if err != nil {
		return err
	}
	rec, err := startRecording(o.out, o.force, "live", o.console.String(),
		map[string]string{"interface": o.iface, "filter": o.filter}, out)
	if err != nil {
		return err
	}
	rec.meta.Ports = cfg.Ports
	e, err := newEngine(&o.af, cfg, rec.idx, out)
	if err != nil {
		return errors.Join(err, rec.idx.Close(), rec.dir.Close())
	}
	runErr := liveSession(ctx, o, rec, e, in, out)
	return errors.Join(runErr, rec.finish(e))
}

// liveSession runs tcpdump writing pcap to stdout; the stream is copied
// byte for byte into capture.pcap (so evidence offsets match) and decoded.
func liveSession(ctx context.Context, o liveOptions, rec *recording, e *engine, in io.Reader, out io.Writer) error {
	capFile, err := rec.dir.Create(session.FileCapture)
	if err != nil {
		return err
	}
	defer func() { _ = capFile.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// -U: write each packet as it arrives; -w -: pcap to stdout; -s 0: whole packets.
	cmd := exec.CommandContext(ctx, o.tcpdump, "-i", o.iface, "-U", "-w", "-", "-s", "0", o.filter) //nolint:gosec // G204: the operator chooses tcpdump and its filter
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("live: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("live: start %s: %w (install tcpdump or pass -tcpdump)", o.tcpdump, err)
	}
	fmt.Fprintf(out, "recording on %s with %q. Type a label and Enter to mark, :help for commands.\n", o.iface, o.filter)
	frames := make(chan flow.Frame, frameQueue)
	srcDone := make(chan error, 1)
	var wg sync.WaitGroup
	safe.Go(&wg, func(err error) { srcDone <- err }, func() {
		srcDone <- readFrames(ctx, io.TeeReader(stdout, capFile), frames)
	})
	cmds := make(chan command)
	go readCommands(ctx, in, cmds) // ends with stdin (see readCommands)
	runErr := e.liveLoop(ctx, frames, cmds, srcDone)
	cancel()
	wg.Wait()
	e.drain(frames)
	waitErr := cmd.Wait()
	if runErr != nil || (waitErr != nil && ctx.Err() == nil) {
		return tcpdumpError(errors.Join(runErr, waitErr), stderr.String())
	}
	return nil
}

// readFrames decodes a pcap stream into frames until it ends.
func readFrames(ctx context.Context, r io.Reader, frames chan<- flow.Frame) error {
	rd, err := pcapfile.NewReader(bufio.NewReader(r))
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("live: tcpdump output: %w", err)
	}
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("live: tcpdump output: %w", err)
		}
		fr := flow.Frame{At: rec.At, LinkType: rec.LinkType, Data: bytes.Clone(rec.Data),
			Evidence: flow.Evidence{File: session.FileCapture, Offset: rec.Offset, Packet: rec.Index}}
		select {
		case frames <- fr:
		case <-ctx.Done():
			return nil
		}
	}
}

func tcpdumpError(err error, stderr string) error {
	hint := ""
	if strings.Contains(stderr, "ermission") || strings.Contains(stderr, "Operation not permitted") {
		hint = " (capturing needs root: run with sudo, or install Wireshark's ChmodBPF; see docs/tools/mixcapture.md)"
	}
	return fmt.Errorf("live: tcpdump: %w: %s%s", err, strings.TrimSpace(stderr), hint)
}
