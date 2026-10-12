// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package session owns a capture session directory:
//
//	session.json   what was recorded, how, when, and the privacy note
//	capture.pcap   every packet (or the operator's own pcap/pcapng, copied)
//	index.jsonl    one line per decoded message, mark and snapshot
//	report.md, keys.csv, harvest.csv, marks.csv   written by the report step
//
// Captures may contain personal data (channel and device names often name
// people), so a session directory is refused inside the public mixcore
// repository; the private research repository is the place for them.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File names inside a session directory.
const (
	FileMeta    = "session.json"
	FileCapture = "capture.pcap"
	FileIndex   = "index.jsonl"
	dirPerm     = 0o750
	publicRepo  = "module github.com/mixproto/mixcore"
)

// PrivacyNote is shown when a recording starts and written into every
// session and report.
const PrivacyNote = "Capture files may contain personal data: channel, scene and device names often " +
	"name people, plus IP and MAC addresses. Keep them in the private research repository " +
	"(research/captures/<console>/<date>/), never in the public mixcore repository, and share " +
	"only the facts you extract."

// ErrPublicRepo is returned for a session directory inside the mixcore repository.
var ErrPublicRepo = errors.New("session: refusing to store a capture inside the public mixcore repository")

// Meta describes a session.
type Meta struct {
	Tool     string            `json:"tool"`
	Mode     string            `json:"mode"` // relay, live, read, introspect
	Console  string            `json:"console"`
	Ports    []uint16          `json:"console_ports,omitempty"` // console ports analyzed (empty: all)
	Started  time.Time         `json:"started"`
	Capture  string            `json:"capture"` // capture file name inside the directory
	Privacy  string            `json:"privacy"`
	Settings map[string]string `json:"settings,omitempty"`
}

// Dir is an open session directory. Files can only be created inside it.
type Dir struct {
	path string
	root *os.Root
}

// Create makes (or reuses) the session directory at path.
func Create(path string) (*Dir, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	if err := CheckLocation(abs); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, dirPerm); err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	return Open(abs)
}

// Open opens an existing session directory.
func Open(path string) (*Dir, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	return &Dir{path: path, root: root}, nil
}

// Path is the directory path.
func (d *Dir) Path() string { return d.path }

// Close releases the directory.
func (d *Dir) Close() error { return d.root.Close() }

// Create creates (truncates) a file in the directory.
func (d *Dir) Create(name string) (*os.File, error) {
	f, err := d.root.Create(name)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	return f, nil
}

// OpenFile opens a file of the directory for reading.
func (d *Dir) OpenFile(name string) (*os.File, error) {
	f, err := d.root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	return f, nil
}

// WriteFile writes a whole file.
func (d *Dir) WriteFile(name string, data []byte) error {
	f, err := d.Create(name)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	return errors.Join(werr, f.Close())
}

// Chown gives every file of the directory to uid:gid, through the root so a
// symlink cannot lead outside it.
func (d *Dir) Chown(uid, gid int) error {
	return fs.WalkDir(d.root.FS(), ".", func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return d.root.Lchown(p, uid, gid)
	})
}

// WriteMeta writes session.json.
func (d *Dir) WriteMeta(m Meta) error {
	m.Privacy = PrivacyNote
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}
	return d.WriteFile(FileMeta, append(b, '\n'))
}

// ReadMeta reads session.json.
func (d *Dir) ReadMeta() (Meta, error) {
	f, err := d.OpenFile(FileMeta)
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = f.Close() }()
	var m Meta
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return Meta{}, fmt.Errorf("session: %s: %w", FileMeta, err)
	}
	return m, nil
}

// CheckLocation refuses a directory inside the public mixcore repository
// (a parent holding a go.mod of module github.com/mixproto/mixcore).
func CheckLocation(abs string) error {
	for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
		if isMixcoreModule(filepath.Join(dir, "go.mod")) {
			return fmt.Errorf("%w (%s); use the private research repository", ErrPublicRepo, dir)
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}

func isMixcoreModule(goMod string) bool {
	f, err := os.Open(goMod) //nolint:gosec // G304: reads go.mod files of the operator's own parent directories
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(io.LimitReader(f, 1<<16))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == publicRepo {
			return true
		}
	}
	return false
}
