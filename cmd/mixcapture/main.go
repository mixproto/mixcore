// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command mixcapture maps a console's network protocol by watching the
// traffic between the console and its own vendor app or surface. It records
// every message (pcap plus a JSONL index), decodes it into keys (OSC address,
// MIDI NRPN, JSON path, text address, binary signature), attributes the keys
// that change to the labels the operator types while working the console,
// diffs snapshots, harvests the initial sync, fits value laws and exports
// tables for docs/protocols as [CAPTURE] facts. Runbook: docs/tools/mixcapture.md.
//
//	mixcapture relay      -console 192.168.1.62 -listen 192.168.2.1 -udp 10024 -out DIR
//	mixcapture live       -i bridge100 -console 192.168.1.62 -out DIR        (needs tcpdump, usually sudo)
//	mixcapture read       -pcap mirror.pcapng [-console IP] [-marks marks.txt] -out DIR
//	mixcapture report     -session DIR
//	mixcapture law        -session DIR -key /ch/01/mix/fader [-p 0.5=-10 ...]
//	mixcapture introspect -family yamaha-rcp -console 192.168.1.70 -out DIR
//
// Safety: mixcapture never originates traffic to a console. The relay
// forwards only what the vendor app sent; introspect sends only the
// read-only queries of docs/protocols/yamaha-rcp.md. There is no write mode.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const exitUsage = 2

var errUsage = errors.New("mixcapture: usage")

const usage = `mixcapture: map a console protocol from its own app's traffic (docs/tools/mixcapture.md)

commands:
  relay       forward a vendor app's UDP/TCP traffic to the console and record it (option B)
  live        record with tcpdump on an interface, e.g. the Internet Sharing bridge (option A)
  read        analyze a pcap or pcapng file captured elsewhere, e.g. on a mirror port (option C)
  report      re-analyze a session directory and rewrite its report (other decoder flags allowed)
  law         fit a value law for one key from labelled marks and/or -p points
  introspect  ask a self-describing console for its parameter table (Yamaha RCP, read-only)

Run "mixcapture <command> -h" for the flags of a command.
While recording, type a label and Enter to mark ("ch1 fader = -10 dB"), Enter alone for an
unlabelled mark, ":s [label]" for a snapshot, ":help" for every command.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := runMain(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(exitUsage)
		}
		os.Exit(1)
	}
}

// runMain is main without os.Exit, for tests.
func runMain(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	return runWith(ctx, args, in, out, systemNet())
}

// runWith runs a command with the given network (an in-memory one in unit tests).
func runWith(ctx context.Context, args []string, in io.Reader, out io.Writer, nw netOps) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return fmt.Errorf("%w: no command", errUsage)
	}
	cmds := map[string]func(context.Context, []string, io.Reader, io.Writer) error{
		"relay": func(ctx context.Context, a []string, in io.Reader, out io.Writer) error {
			return runRelay(ctx, a, in, out, nw)
		},
		"live": runLive, "read": runRead, "report": runReport, "law": runLaw,
		"introspect": func(ctx context.Context, a []string, in io.Reader, out io.Writer) error {
			return runIntrospect(ctx, a, in, out, nw)
		},
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprint(out, usage)
		return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}
	return cmd(ctx, args[1:], in, out)
}
