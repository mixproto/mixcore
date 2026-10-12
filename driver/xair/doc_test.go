// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var codeCitation = regexp.MustCompile(`((?:driver|sim|api|console)/[\w/]+\.go):(\d+)`)

// Every code citation of docs/protocols/xair.md points at a file that
// exists and has that line. Section 12 is exempt: it is a dated reading of
// driver/x32 at d26cf25, before the engine moved to driver/internal/xosc.
func TestProtocolDoc_CodeCitationsExist(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "protocols", "xair.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	if i := strings.Index(text, "## 12. "); i >= 0 {
		if j := strings.Index(text[i+1:], "\n## "); j >= 0 {
			text = text[:i] + text[i+1+j:]
		}
	}
	for _, m := range codeCitation.FindAllStringSubmatch(text, -1) {
		src, err := os.ReadFile(filepath.Join("..", "..", m[1])) //nolint:gosec // G304: a repo path from the doc
		if err != nil {
			t.Errorf("%s:%s cites a missing file", m[1], m[2])
			continue
		}
		line, _ := strconv.Atoi(m[2])
		if n := bytes.Count(src, []byte("\n")); line > n {
			t.Errorf("%s:%s cites line %d of a %d-line file", m[1], m[2], line, n)
		}
	}
}
