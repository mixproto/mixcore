// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/mixproto/mixcore/internal/capture/session"
)

const toolName = "mixcapture"

// recording is an open session directory with its index.
type recording struct {
	dir  *session.Dir
	idx  *session.IndexWriter
	meta session.Meta
	out  io.Writer
}

// startRecording creates the session directory, warns about personal data
// and opens the index.
func startRecording(outDir, mode, console string, settings map[string]string, out io.Writer) (*recording, error) {
	if outDir == "" {
		return nil, fmt.Errorf("%w: -out is required (a directory in the private research repository, "+
			"e.g. research/captures/<console>/<date>)", errUsage)
	}
	d, err := session.Create(outDir)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "PRIVACY: %s\n", session.PrivacyNote)
	meta := session.Meta{Tool: toolName, Mode: mode, Console: console, Started: time.Now().UTC(),
		Capture: session.FileCapture, Settings: settings}
	if err := d.WriteMeta(meta); err != nil {
		_ = d.Close()
		return nil, err
	}
	f, err := d.Create(session.FileIndex)
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	fmt.Fprintf(out, "session: %s\n", d.Path())
	return &recording{dir: d, idx: session.NewIndexWriter(f), meta: meta, out: out}, nil
}

// finish completes the analysis, writes the report and closes everything.
func (r *recording) finish(e *engine) error {
	errs := []error{e.finish(), e.writeReport(r.dir, r.meta), r.idx.Close(), r.dir.WriteMeta(r.meta)}
	errs = append(errs, giveBackToSudoUser(r.dir), r.dir.Close())
	return errors.Join(errs...)
}

// giveBackToSudoUser hands the session files to the user who ran sudo
// (tcpdump on macOS needs root), so they do not end up owned by root.
func giveBackToSudoUser(d *session.Dir) error {
	uidText, gidText := os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID")
	if os.Geteuid() != 0 || uidText == "" || gidText == "" {
		return nil
	}
	uid, err1 := strconv.Atoi(uidText)
	gid, err2 := strconv.Atoi(gidText)
	if err1 != nil || err2 != nil {
		return nil
	}
	return d.Chown(uid, gid)
}
