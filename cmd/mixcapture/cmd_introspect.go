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
	"time"

	"github.com/mixproto/mixcore/internal/capture/rcpinfo"
	"github.com/mixproto/mixcore/internal/capture/report"
	"github.com/mixproto/mixcore/internal/capture/session"
)

const (
	familyYamahaRCP = "yamaha-rcp"
	dialTimeout     = 3 * time.Second
	progressEvery   = 250
)

type introspectOptions struct {
	console netip.AddrPort
	out     string
	force   bool
	max     int
	timeout time.Duration
}

func parseIntrospect(args []string, out io.Writer) (introspectOptions, error) {
	var o introspectOptions
	fs := newFlagSet("introspect", out)
	family := fs.String("family", "", "console family with self-description: "+familyYamahaRCP+" (required)")
	consoleText := fs.String("console", "", "console IP address (required)")
	port := fs.Int("port", rcpinfo.Port, "console TCP port (docs/protocols/yamaha-rcp.md YR-F01)")
	fs.StringVar(&o.out, "out", "", "session directory (in the private research repository)")
	fs.BoolVar(&o.force, "force", false, "overwrite a session already in -out")
	fs.IntVar(&o.max, "max", rcpinfo.DefaultMaxIndex, "largest number of table entries to ask for")
	fs.DurationVar(&o.timeout, "timeout", rcpinfo.DefaultReplyTimeout, "reply timeout per query")
	if err := parseFlags(fs, args); err != nil {
		return o, err
	}
	if *family != familyYamahaRCP {
		return o, fmt.Errorf("%w: -family must be %s (the only self-describing family documented so far)", errUsage, familyYamahaRCP)
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
func runIntrospect(ctx context.Context, args []string, _ io.Reader, out io.Writer) error {
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
	fmt.Fprintf(out, "PRIVACY: %s\nsession: %s\nread-only queries only: devinfo, prminfo, mtrinfo\n", session.PrivacyNote, d.Path())
	meta := session.Meta{Tool: toolName, Mode: "introspect", Console: o.console.String(), Started: time.Now().UTC(),
		Capture: session.FileCapture, Settings: map[string]string{"family": familyYamahaRCP}}
	runErr := introspect(ctx, o, d, meta, out)
	return errors.Join(runErr, d.WriteMeta(meta), d.Close())
}

func introspect(ctx context.Context, o introspectOptions, d *session.Dir, meta session.Meta, out io.Writer) error {
	capFile, err := d.Create(session.FileCapture)
	if err != nil {
		return err
	}
	defer func() { _ = capFile.Close() }()
	rec, err := session.NewRecorder(capFile, session.FileCapture, nil)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", o.console.String())
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
