// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package testkit holds test helpers shared by driver tests: a wire tap that
// records every packet a driver sends, and golden-file comparison. It is
// imported only from _test.go files.
package testkit

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
)

// ListenFunc matches driver.ListenPacketFunc without importing driver.
type ListenFunc = func(ctx context.Context, network, address string) (net.PacketConn, error)

type tap struct {
	net.PacketConn
	mu   *sync.Mutex
	sent []string
}

func (t *tap) WriteTo(b []byte, to net.Addr) (int, error) {
	t.mu.Lock()
	t.sent = append(t.sent, hex.EncodeToString(b))
	t.mu.Unlock()
	return t.PacketConn.WriteTo(b, to)
}

// WireTrace taps the sockets a driver opens, in the order it opens them.
type WireTrace struct {
	mu      sync.Mutex
	sockets []*tap
}

// Listen wraps inner so that every socket it opens is tapped.
func (w *WireTrace) Listen(inner ListenFunc) ListenFunc {
	return func(ctx context.Context, network, address string) (net.PacketConn, error) {
		pc, err := inner(ctx, network, address)
		if err != nil {
			return nil, err
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		t := &tap{PacketConn: pc, mu: &w.mu}
		w.sockets = append(w.sockets, t)
		return t, nil
	}
}

// Take returns the packets sent per socket since the last Take, as hex.
func (w *WireTrace) Take() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][]string, len(w.sockets))
	for i, s := range w.sockets {
		out[i] = slices.Clone(s.sent)
		s.sent = s.sent[:0]
	}
	return out
}

// Render writes the ordered connect packets of each socket and the sorted
// distinct packets of the live phase, one hex packet per line with its
// decoded form. names label the sockets in open order.
func Render(connect, live [][]string, names []string) string {
	name := func(i int) string { return names[min(i, len(names)-1)] }
	var b strings.Builder
	for i, pkts := range connect {
		b.WriteString("## connect, socket " + name(i) + "\n")
		for _, p := range pkts {
			b.WriteString(describe(p))
		}
	}
	for i, pkts := range live {
		b.WriteString("## live distinct, socket " + name(i) + "\n")
		set := slices.Clone(pkts)
		slices.Sort(set)
		for _, p := range slices.Compact(set) {
			b.WriteString(describe(p))
		}
	}
	return b.String()
}

func describe(h string) string {
	raw, _ := hex.DecodeString(h)
	var m osc.Message
	text := "undecodable"
	if osc.Decode(raw, &m) == nil {
		text = m.String()
	}
	return h + "  " + text + "\n"
}

// Golden compares got with the file at path; update rewrites the file first.
func Golden(t *testing.T, path, got string, update bool) {
	t.Helper()
	if update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // G304: a fixture path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("wire trace changed; first difference against %s:\n%s", path, firstDiff(string(want), got))
	}
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + strconv.Itoa(i+1) + ":\n- " + a + "\n+ " + b
		}
	}
	return "(no line differs)"
}
