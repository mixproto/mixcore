// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/rcpinfo"
	"github.com/mixproto/mixcore/internal/capture/report"
	"github.com/mixproto/mixcore/internal/capture/session"
	"github.com/mixproto/mixcore/internal/capture/winginfo"
)

const (
	familyYamahaRCP = "yamaha-rcp"
	familyWing      = "wing"
	dialTimeout     = 3 * time.Second
	progressEvery   = 250
)

type introspectOptions struct {
	family  string
	console netip.AddrPort
	out     string
	force   bool
	max     int
	timeout time.Duration
	roots   []string // wing: walk only below these paths
	values  bool     // wing: also read each leaf's value
	// valuesCtl: wing: also read /$ctl and action values (default: skipped)
	valuesCtl bool
}

func parseIntrospect(args []string, out io.Writer) (introspectOptions, error) {
	var o introspectOptions
	fs := newFlagSet("introspect", out)
	family := fs.String("family", "", "console family with self-description: "+familyYamahaRCP+" or "+familyWing+" (required)")
	consoleText := fs.String("console", "", "console IP address (required)")
	port := fs.Int("port", 0, "console TCP port (default: yamaha-rcp 49280, YR-F01; wing 2222, WG-F10)")
	roots := fs.String("roots", "", "wing: comma-separated paths to walk below (default: the whole tree)")
	fs.BoolVar(&o.values, "values", true, "wing: also read every leaf's value (data requests only)")
	fs.BoolVar(&o.valuesCtl, "values-ctl", false, "wing: also read values under /$ctl and of action nodes (side effects of reads unverified)")
	fs.StringVar(&o.out, "out", "", "session directory (in the private research repository)")
	fs.BoolVar(&o.force, "force", false, "overwrite a session already in -out")
	fs.IntVar(&o.max, "max", 0, "largest number of table entries (yamaha-rcp, default 20000) or nodes (wing, default 60000)")
	fs.DurationVar(&o.timeout, "timeout", rcpinfo.DefaultReplyTimeout, "reply timeout per query")
	if err := parseFlags(fs, args); err != nil {
		return o, err
	}
	defaults := map[string][2]int{familyYamahaRCP: {rcpinfo.Port, rcpinfo.DefaultMaxIndex}, familyWing: {winginfo.Port, winginfo.DefaultMaxNodes}}
	def, ok := defaults[*family]
	if !ok {
		return o, fmt.Errorf("%w: -family must be %s or %s (the self-describing families documented so far)", errUsage, familyYamahaRCP, familyWing)
	}
	o.family = *family
	if *port == 0 {
		*port = def[0]
	}
	if o.max == 0 {
		o.max = def[1]
	}
	if *roots != "" {
		o.roots = strings.Split(*roots, ",")
	}
	ip, err := parseConsoleIP(*consoleText)
	if err != nil {
		return o, err
	}
	if *port < 1 || *port > 65535 || o.max < 1 {
		return o, fmt.Errorf("%w: -port 1..65535 and -max >= 1", errUsage)
	}
	o.console = netip.AddrPortFrom(ip, uint16(*port))
	return o, nil
}

// runIntrospect asks the console for its own tables with read-only queries
// only (rcpinfo refuses anything else) and records the conversation.
func runIntrospect(ctx context.Context, args []string, _ io.Reader, out io.Writer, nw netOps) error {
	o, err := parseIntrospect(args, out)
	if err != nil {
		return err
	}
	if o.out == "" {
		return fmt.Errorf("%w: -out is required (a directory in the private research repository)", errUsage)
	}
	d, err := session.Create(o.out, o.force)
	if err != nil {
		return err
	}
	meta := session.Meta{Tool: toolName, Mode: "introspect", Console: o.console.String(), Started: time.Now().UTC(),
		Capture: session.FileCapture, Settings: map[string]string{"family": o.family}}
	var runErr error
	if o.family == familyWing {
		fmt.Fprintf(out, "PRIVACY: %s\nsession: %s\nread-only requests only: DA DD, D7 <hash> DD, D7 <hash> DC\n", session.PrivacyNote, d.Path())
		runErr = introspectWing(ctx, o, nw, d, out)
	} else {
		fmt.Fprintf(out, "PRIVACY: %s\nsession: %s\nread-only queries only: devinfo, prminfo, mtrinfo\n", session.PrivacyNote, d.Path())
		runErr = introspect(ctx, o, nw, d, meta, out)
	}
	return errors.Join(runErr, d.WriteMeta(meta), d.Close())
}

func introspect(ctx context.Context, o introspectOptions, nw netOps, d *session.Dir, meta session.Meta, out io.Writer) error {
	capFile, err := d.Create(session.FileCapture)
	if err != nil {
		return err
	}
	defer func() { _ = capFile.Close() }()
	rec, err := session.NewRecorder(capFile, session.FileCapture, nil)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, err := nw.dial(dctx, "tcp", o.console.String())
	cancel()
	if err != nil {
		return fmt.Errorf("introspect: connect %s: %w", o.console, err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	c := rcpinfo.NewClient(session.TapConn(conn, rec), o.timeout)
	device := map[string]string{}
	for _, what := range []string{"productname", "devicename"} {
		v, err := c.DevInfo(what)
		if err != nil {
			v = "(" + err.Error() + ")"
		}
		device[what] = v
		fmt.Fprintf(out, "devinfo %s: %s\n", what, v)
	}
	var tables []report.Table
	for _, verb := range []string{rcpinfo.VerbPrmInfo, rcpinfo.VerbMtrInfo} {
		entries, why, err := c.Table(ctx, verb, o.max, func(n int) {
			if n%progressEvery == 0 {
				fmt.Fprintf(out, "%s: %d entries\n", verb, n)
			}
		})
		if err != nil {
			return fmt.Errorf("introspect %s: %w", verb, err)
		}
		fmt.Fprintf(out, "%s: %d entries, stopped: %s\n", verb, len(entries), why)
		tables = append(tables, report.Table{Verb: verb, Entries: entries, Stopped: why})
	}
	if err := report.WriteIntrospection(d, meta, device, tables); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s, %s and %s in %s\n", report.FileParams, report.FileMeters, report.FileIntroMD, d.Path())
	return nil
}
