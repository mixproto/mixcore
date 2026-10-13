// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestIntrospectWing_UnitPipe walks part of sim/wing through the command
// and checks the files and that the console saw no write.
func TestIntrospectWing_UnitPipe(t *testing.T) {
	r := newMemRig(t)
	sim := simwing.New(simwing.Options{Model: "wing-compact"})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() { cancel(); sim.DropConnections(); wg.Wait() }()
	ops := r.ops()
	ops.dial = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		wg.Go(func() { sim.ServeConn(ctx, server) })
		return client, nil
	}
	dir := filepath.Join(t.TempDir(), "wing")
	var out syncBuf
	err := runWith(context.Background(), []string{"introspect", "-family", "wing", "-console", memConsole,
		"-roots", "/ch/1,/$syscfg", "-out", dir}, strings.NewReader(""), &out, ops)
	if err != nil {
		t.Fatalf("introspect: %v\n%s", err, out.String())
	}
	if csv := readFile(t, filepath.Join(dir, fileWingSchema)); !strings.Contains(csv, "/ch/1/fdr,38ae75c2,") {
		t.Fatalf("schema.csv:\n%s", csv[:min(len(csv), 400)])
	}
	if md := readFile(t, filepath.Join(dir, fileWingMD)); !strings.Contains(md, `"wing-compact"`) {
		t.Fatalf("introspect.md:\n%s", md)
	}
	if w := sim.Writes(); len(w) != 0 {
		t.Fatalf("introspection wrote to the console: %+v", w)
	}
}
