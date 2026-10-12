// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/analyze"
	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
)

// analysisFlags are shared by every command that decodes traffic.
type analysisFlags struct {
	window   time.Duration
	quiet    time.Duration
	decoders string
	ports    string
	binKey   int
	sysexKey int
}

func (a *analysisFlags) register(fs *flag.FlagSet) {
	fs.DurationVar(&a.window, "window", analyze.DefaultMarkWindow, "how long after a mark changes are attributed to it")
	fs.DurationVar(&a.quiet, "quiet", analyze.DefaultHarvestQuiet, "initial-sync harvest ends after no new key for this long")
	fs.StringVar(&a.decoders, "decoder", "", "force decoders per console port, e.g. 10024=osc,49280=text (osc, midi, text, json, binary)")
	fs.StringVar(&a.ports, "ports", "", "analyze only these console ports, e.g. 10024,51325 (default: all)")
	fs.IntVar(&a.binKey, "bin-key", decode.DefaultBinaryKeyBytes, "leading frame bytes that form a binary key")
	fs.IntVar(&a.sysexKey, "sysex-key", decode.DefaultSysExKeyBytes, "bytes after F0 that form a SysEx key")
}

// flowConfig turns the flags into a flow.Config for console.
func (a *analysisFlags) flowConfig(console netip.Addr) (flow.Config, error) {
	cfg := flow.Config{Console: console, Decode: decode.Options{BinaryKeyBytes: a.binKey, SysExKeyBytes: a.sysexKey}}
	var err error
	if cfg.Ports, err = parsePortList(a.ports); err != nil {
		return cfg, err
	}
	if a.decoders == "" {
		return cfg, nil
	}
	cfg.Force = map[uint16]string{}
	for item := range strings.SplitSeq(a.decoders, ",") {
		p, name, ok := strings.Cut(strings.TrimSpace(item), "=")
		port, perr := parsePort(p)
		if !ok || perr != nil {
			return cfg, fmt.Errorf("%w: -decoder %q: want port=name", errUsage, item)
		}
		cfg.Force[port] = name
	}
	return cfg, nil
}

func (a *analysisFlags) analyzeConfig(notify func(analyze.Event)) analyze.Config {
	return analyze.Config{MarkWindow: a.window, HarvestQuiet: a.quiet, Notify: notify}
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("%w: bad port %q", errUsage, s)
	}
	return uint16(n), nil
}

func parsePortList(s string) ([]uint16, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []uint16
	for p := range strings.SplitSeq(s, ",") {
		n, err := parsePort(p)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%w: bad port list %q", errUsage, s)
		}
		out = append(out, n)
	}
	return out, nil
}

// portMap is "listen:console" or one port used for both.
type portMap struct{ listen, console uint16 }

func parsePortMaps(s string) ([]portMap, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []portMap
	for item := range strings.SplitSeq(s, ",") {
		l, c, isMap := strings.Cut(strings.TrimSpace(item), ":")
		lp, err := parsePort(l)
		if err != nil {
			return nil, err
		}
		cp := lp
		if isMap {
			if cp, err = parsePort(c); err != nil {
				return nil, err
			}
		}
		if cp == 0 {
			return nil, fmt.Errorf("%w: console port 0 in %q", errUsage, item)
		}
		out = append(out, portMap{listen: lp, console: cp})
	}
	return out, nil
}

func parseConsoleIP(s string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: -console must be the console's IP address, got %q", errUsage, s)
	}
	return ip.Unmap(), nil
}

func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("mixcapture "+name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected argument %q", errUsage, fs.Arg(0))
	}
	return nil
}

// localIPv4 lists this machine's IPv4 addresses (hint for -listen).
func localIPv4() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
			out = append(out, p.Addr().String())
		}
	}
	return out
}
