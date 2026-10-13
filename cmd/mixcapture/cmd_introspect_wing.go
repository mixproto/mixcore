// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mixproto/mixcore/internal/capture/session"
	"github.com/mixproto/mixcore/internal/capture/winginfo"
)

// Files of a WING introspection.
const (
	fileWingSchema = "schema.csv"
	fileWingMD     = "introspect.md"
)

// introspectWing walks a WING's node definitions with read-only requests
// (winginfo cannot encode anything else) and records the conversation.
func introspectWing(ctx context.Context, o introspectOptions, nw netOps, d *session.Dir, out io.Writer) error {
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
	c := winginfo.NewClient(session.TapConn(conn, rec), o.timeout)
	skip := winginfo.DefaultSkipValue
	if o.valuesCtl {
		skip = nil
	}
	nodes, stopped, walkErr := winginfo.Walk(ctx, c, winginfo.Options{Roots: o.roots, MaxNodes: o.max, Values: o.values, SkipValue: skip,
		Progress: func(n int) {
			if n%progressEvery == 0 {
				fmt.Fprintf(out, "%d nodes\n", n)
			}
		}})
	fmt.Fprintf(out, "%d nodes, stopped: %s\n", len(nodes), stopped)
	table, err := winginfo.CSV(nodes)
	if err != nil {
		return err
	}
	if err := d.WriteFile(fileWingSchema, table); err != nil {
		return err
	}
	if err := d.WriteFile(fileWingMD, []byte(winginfo.Markdown(o.console.String(), session.PrivacyNote, stopped, nodes))); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s and %s in %s\n", fileWingSchema, fileWingMD, d.Path())
	return walkErr
}
