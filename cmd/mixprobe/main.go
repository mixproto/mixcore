// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command mixprobe runs a guided hardware session against a real X32/M32
// family console on a lab network. It identifies the console, snapshots every
// parameter it may change, checks the facts of docs/protocols/x32.md that
// software can check, asks the operator for physical steps (move a fader,
// recall a scene, unplug the cable, reboot), restores the snapshot (also on
// Ctrl-C) and writes report.md and report.json.
//
//	mixprobe --lab -host 192.168.1.62 [-channels 1-8] [-mutegroup 6] [-out dir]
//	mixprobe --lab -discover
//
// Safety: it refuses to run without --lab, asks for the amplifiers to be off
// first, changes only the faders and mutes of the chosen input channels and
// one mute group, moves faders only down from their snapshot value (and
// back), only mutes (never unmutes beyond the snapshot) and only engages the
// mute group (never releases one the snapshot had engaged).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
)

const (
	defaultChannels  = "1-8"
	defaultMuteGroup = 6
	maxChannel       = 32
	maxMuteGroup     = 6
	discoverWait     = 2 * time.Second
	exitUsage        = 2
)

var errUsage = errors.New("mixprobe: usage")

func main() {
	// Every catchable way a terminal session ends restores the snapshot.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	if err := runMain(ctx, os.Args[1:], os.Stdin, os.Stdout, osListen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(exitUsage)
		}
		os.Exit(1)
	}
}

func osListen(ctx context.Context, network, address string) (net.PacketConn, error) {
	var lc net.ListenConfig
	return lc.ListenPacket(ctx, network, address)
}

// options are the parsed command line.
type options struct {
	lab       bool
	host      string
	port      int
	discover  bool
	channels  []int
	muteGroup int
	outDir    string
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("mixprobe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	var chans string
	fs.BoolVar(&o.lab, "lab", false, "required: confirms this console is on a lab network with the amplifiers off")
	fs.StringVar(&o.host, "host", "", "console IP address")
	fs.IntVar(&o.port, "port", x32.DefaultPort, "console UDP port")
	fs.BoolVar(&o.discover, "discover", false, "find the console with an /xinfo broadcast instead of -host")
	fs.StringVar(&chans, "channels", defaultChannels, "input channels mixprobe may change, e.g. 1-8 or 1,3,5")
	fs.IntVar(&o.muteGroup, "mutegroup", defaultMuteGroup, "mute group mixprobe may engage (1-6)")
	fs.StringVar(&o.outDir, "out", "", "report directory (default mixprobe-<time>)")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w: %w", errUsage, err)
	}
	if !o.lab {
		return o, fmt.Errorf("%w: refusing to run without --lab (a lab console, amplifiers off)", errUsage)
	}
	var err error
	if o.channels, err = parseChannels(chans); err != nil {
		return o, err
	}
	if o.muteGroup < 1 || o.muteGroup > maxMuteGroup {
		return o, fmt.Errorf("%w: -mutegroup %d is not 1..%d", errUsage, o.muteGroup, maxMuteGroup)
	}
	if (o.host == "") == !o.discover {
		return o, fmt.Errorf("%w: give exactly one of -host or -discover", errUsage)
	}
	if o.outDir == "" {
		o.outDir = "mixprobe-" + time.Now().Format("20060102-150405")
	}
	return o, nil
}

// parseChannels accepts "1-8", "3" and "1,3,5" with channels 1..32.
func parseChannels(s string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		a, err1 := strconv.Atoi(lo)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(hi)
		}
		if err1 != nil || err2 != nil || a < 1 || b > maxChannel || a > b {
			return nil, fmt.Errorf("%w: bad -channels %q", errUsage, s)
		}
		for ch := a; ch <= b; ch++ {
			out = append(out, ch)
		}
	}
	return out, nil
}

// runMain is main without os.Exit, for tests.
func runMain(ctx context.Context, args []string, in io.Reader, out io.Writer, listen driver.ListenPacketFunc) error {
	o, err := parseArgs(args, out)
	if err != nil {
		return err
	}
	console, err := findConsole(ctx, o, listen, out)
	if err != nil {
		return err
	}
	cfg := config{console: console, channels: o.channels, muteGroup: o.muteGroup, listen: listen,
		prompt: newTerminal(in, out), out: out}
	return runAndReport(ctx, cfg, o.outDir)
}

func runAndReport(ctx context.Context, cfg config, outDir string) error {
	cfg.outDir = outDir
	rep, runErr := run(ctx, cfg)
	if err := rep.write(outDir); err != nil {
		return errors.Join(runErr, err)
	}
	fmt.Fprintf(cfg.out, "\nReport: %s/report.md and report.json\n", outDir)
	return runErr
}

func findConsole(ctx context.Context, o options, listen driver.ListenPacketFunc, out io.Writer) (netip.AddrPort, error) {
	if !o.discover {
		ip, err := netip.ParseAddr(o.host)
		if err != nil || o.port < 1 || o.port > 65535 {
			return netip.AddrPort{}, fmt.Errorf("%w: -host must be an IP address and -port 1..65535", errUsage)
		}
		return netip.AddrPortFrom(ip.Unmap(), uint16(o.port)), nil
	}
	found, err := x32.Probe(ctx, listen, x32.BroadcastTargets(), discoverWait)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("mixprobe: discovery: %w", err)
	}
	for _, f := range found {
		fmt.Fprintf(out, "found %s %q at %s:%d\n", f.Identity.Model, f.Identity.Name, f.Endpoint.Host, f.Endpoint.Port)
	}
	if len(found) != 1 {
		return netip.AddrPort{}, fmt.Errorf("%w: discovery found %d consoles; run again with -host", errUsage, len(found))
	}
	ip, err := netip.ParseAddr(found[0].Endpoint.Host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("mixprobe: discovered address: %w", err)
	}
	return netip.AddrPortFrom(ip, uint16(found[0].Endpoint.Port)), nil
}
