// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/relay"
	"github.com/mixproto/mixcore/internal/capture/session"
	"github.com/mixproto/mixcore/internal/safe"
)

type relayOptions struct {
	af      analysisFlags
	console netip.Addr
	listen  netip.Addr
	udp     []portMap
	tcp     []portMap
	out     string
}

func parseRelay(args []string, out io.Writer) (relayOptions, error) {
	var o relayOptions
	fs := newFlagSet("relay", out)
	consoleText := fs.String("console", "", "console IP address (required)")
	listenText := fs.String("listen", "", "this Mac's IP on the network the vendor app uses (required; the app connects here)")
	udp := fs.String("udp", "", "UDP ports to relay: 10024 or listen:console, comma separated")
	tcp := fs.String("tcp", "", "TCP ports to relay: 49280 or listen:console, comma separated")
	fs.StringVar(&o.out, "out", "", "session directory (in the private research repository)")
	o.af.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return o, err
	}
	var err error
	if o.console, err = parseConsoleIP(*consoleText); err != nil {
		return o, err
	}
	if o.listen, err = netip.ParseAddr(*listenText); err != nil {
		return o, fmt.Errorf("%w: -listen must be one of this machine's addresses (have %s)", errUsage,
			strings.Join(localIPv4(), ", "))
	}
	if o.udp, err = parsePortMaps(*udp); err != nil {
		return o, err
	}
	if o.tcp, err = parsePortMaps(*tcp); err != nil {
		return o, err
	}
	if len(o.udp)+len(o.tcp) == 0 {
		return o, fmt.Errorf("%w: give -udp and/or -tcp ports (see the console's section in docs/tools/mixcapture.md)", errUsage)
	}
	return o, nil
}

func runRelay(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	o, err := parseRelay(args, out)
	if err != nil {
		return err
	}
	cfg, err := o.af.flowConfig(o.console)
	if err != nil {
		return err
	}
	if len(cfg.Ports) == 0 { // the relay knows the console's ports
		for _, pm := range append(o.udp, o.tcp...) {
			cfg.Ports = append(cfg.Ports, pm.console)
		}
	}
	rec, err := startRecording(o.out, "relay", o.console.String(), map[string]string{
		"listen": o.listen.String(), "udp": fmt.Sprint(o.udp), "tcp": fmt.Sprint(o.tcp)}, out)
	if err != nil {
		return err
	}
	rec.meta.Ports = cfg.Ports
	e, err := newEngine(&o.af, cfg, rec.idx, out)
	if err != nil {
		return errors.Join(err, rec.idx.Close(), rec.dir.Close())
	}
	runErr := relaySession(ctx, o, rec, e, in, out)
	return errors.Join(runErr, rec.finish(e))
}

func relaySession(ctx context.Context, o relayOptions, rec *recording, e *engine, in io.Reader, out io.Writer) error {
	capFile, err := rec.dir.Create(session.FileCapture)
	if err != nil {
		return err
	}
	defer func() { _ = capFile.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan flow.Frame, frameQueue)
	recorder, err := session.NewRecorder(capFile, session.FileCapture, func(fr flow.Frame) {
		select {
		case frames <- fr:
		case <-ctx.Done():
		}
	})
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	srcDone := make(chan error, len(o.udp)+len(o.tcp))
	if err := startRelays(ctx, &wg, o, recorder, srcDone, out); err != nil {
		cancel()
		wg.Wait()
		return err
	}
	fmt.Fprintf(out, "recording; point the vendor app at %s. Type a label and Enter to mark, :help for commands.\n", o.listen)
	cmds := make(chan command)
	go readCommands(ctx, in, cmds) // ends with stdin (see readCommands)
	runErr := e.liveLoop(ctx, frames, cmds, srcDone)
	cancel()
	wg.Wait()
	e.drain(frames)
	return runErr
}

// startRelays binds every port and starts its relay; a bind error stops all.
func startRelays(ctx context.Context, wg *sync.WaitGroup, o relayOptions, rec *session.Recorder, srcDone chan<- error,
	out io.Writer) error {
	report := func(err error) {
		if err == nil || errors.Is(err, relay.ErrClosed) {
			return
		}
		select {
		case srcDone <- err:
		default: // one error is enough to stop the session
		}
	}
	var lc net.ListenConfig
	for _, pm := range o.udp {
		pc, err := lc.ListenPacket(ctx, "udp", netip.AddrPortFrom(o.listen, pm.listen).String())
		if err != nil {
			return fmt.Errorf("relay: listen udp %d: %w", pm.listen, err)
		}
		u := &relay.UDP{Listen: pc, Console: netip.AddrPortFrom(o.console, pm.console), Rec: rec,
			Upstream: func(ctx context.Context) (net.PacketConn, error) { return lc.ListenPacket(ctx, "udp", ":0") }}
		fmt.Fprintf(out, "relay udp %s -> %s\n", pc.LocalAddr(), u.Console)
		safe.Go(wg, report, func() { defer func() { _ = pc.Close() }(); report(u.Serve(ctx)) })
	}
	for _, pm := range o.tcp {
		ln, err := lc.Listen(ctx, "tcp", netip.AddrPortFrom(o.listen, pm.listen).String())
		if err != nil {
			return fmt.Errorf("relay: listen tcp %d: %w", pm.listen, err)
		}
		console := netip.AddrPortFrom(o.console, pm.console)
		var d net.Dialer
		t := &relay.TCP{Listener: ln, Console: console, Rec: rec,
			Dial: func(ctx context.Context) (net.Conn, error) { return d.DialContext(ctx, "tcp", console.String()) }}
		fmt.Fprintf(out, "relay tcp %s -> %s\n", ln.Addr(), console)
		safe.Go(wg, report, func() { report(t.Serve(ctx)) })
	}
	return nil
}
