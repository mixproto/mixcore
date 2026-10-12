// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command mixprobe runs a guided hardware session against a real X32/M32
// family console, or (-family xair) an X Air / M Air mixer, on a lab network.
// It identifies the console, snapshots every parameter it may change, checks
// the facts of docs/protocols/x32.md or xair.md that software can check (for
// xair: the items section 13 marks "mixprobe", read-only ones first), asks
// the operator for physical steps (change a fader, recall a scene, unplug the
// cable, reboot), restores the snapshot (also on Ctrl-C) and writes report.md
// and report.json.
//
//	mixprobe --lab -host 192.168.1.62 [-channels 1-8] [-mutegroup 6] [-out dir]
//	mixprobe --lab -family xair -host 192.168.1.1 [-channels 1-8] [-mutegroup 4]
//	mixprobe --lab [-family xair] -discover
//
// Safety: it refuses to run without --lab, asks for the amplifiers to be off
// first, changes only the faders and mutes of the chosen input channels and
// one mute group, moves faders only down from their snapshot value (and
// back), only mutes (never unmutes beyond the snapshot) and only engages the
// mute group (never releases one the snapshot had engaged). For xair it may
// also lower one bus send of the first allowed channel and rename the last
// allowed channel (both restored).
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
)

const (
	defaultChannels = "1-8"
	defaultFamily   = "x32"
	discoverWait    = 2 * time.Second
	exitUsage       = 2
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
	fam       family
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
	var chans, fam string
	fs.StringVar(&fam, "family", defaultFamily, "console family: x32 or xair")
	fs.BoolVar(&o.lab, "lab", false, "required: confirms this console is on a lab network with the amplifiers off")
	fs.StringVar(&o.host, "host", "", "console IP address")
	fs.IntVar(&o.port, "port", 0, "console UDP port (default: the family's, 10023 or 10024)")
	fs.BoolVar(&o.discover, "discover", false, "find the console with an /xinfo broadcast instead of -host")
	fs.StringVar(&chans, "channels", defaultChannels, "input channels mixprobe may change, e.g. 1-8 or 1,3,5")
	fs.IntVar(&o.muteGroup, "mutegroup", 0, "mute group mixprobe may engage (x32 1-6, default 6; xair 1-4, default 4)")
	fs.StringVar(&o.outDir, "out", "", "report directory (default mixprobe-<time>)")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w: %w", errUsage, err)
	}
	if !o.lab {
		return o, fmt.Errorf("%w: refusing to run without --lab (a lab console, amplifiers off)", errUsage)
	}
	var ok bool
	if o.fam, ok = families[fam]; !ok {
		return o, fmt.Errorf("%w: -family %q is not x32 or xair", errUsage, fam)
	}
	o.port = cmpOr(o.port, o.fam.defaultPort)
	o.muteGroup = cmpOr(o.muteGroup, o.fam.defaultMuteGroup)
	var err error
	if o.channels, err = parseChannels(chans, o.fam.maxChannel); err != nil {
		return o, err
	}
	if o.muteGroup < 1 || o.muteGroup > o.fam.maxMuteGroup {
		return o, fmt.Errorf("%w: -mutegroup %d is not 1..%d", errUsage, o.muteGroup, o.fam.maxMuteGroup)
	}
	if (o.host == "") == !o.discover {
		return o, fmt.Errorf("%w: give exactly one of -host or -discover", errUsage)
	}
	if o.outDir == "" {
		o.outDir = "mixprobe-" + time.Now().Format("20060102-150405")
	}
	return o, nil
}

func cmpOr(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// parseChannels accepts "1-8", "3" and "1,3,5" with channels 1..maxChannel.
func parseChannels(s string, maxChannel int) ([]int, error) {
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
	cfg := config{fam: o.fam, console: console, channels: o.channels, muteGroup: o.muteGroup, listen: listen,
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
	found, err := o.fam.probe(ctx, listen, o.fam.broadcast(), discoverWait)
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
