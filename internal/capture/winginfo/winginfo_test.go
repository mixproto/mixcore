// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package winginfo

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/wingproto"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// TestWalk_ReadOnlyAgainstSim walks /ch/1 and /$syscfg of the simulator,
// with and without a DE after data answers (item 3): the documented hashes
// match and the console logs no write at all.
func TestWalk_ReadOnlyAgainstSim(t *testing.T) {
	for _, de := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			sim := simwing.New(simwing.Options{DataEndsWithDE: de, Model: "wing-rack"})
			client, server := net.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			wg.Go(func() { sim.ServeConn(ctx, server) })
			defer func() { cancel(); _ = client.Close(); wg.Wait() }()
			nodes, stopped, err := Walk(ctx, NewClient(client, 0), Options{Roots: []string{"/ch/1", "/$syscfg"}, Values: true})
			if err != nil || stopped != "done" {
				t.Fatalf("walk: %v %s", err, stopped)
			}
			md := Markdown("10.0.0.1:2222", "privacy", stopped, nodes)
			if !strings.Contains(md, "| /ch/1/fdr | 38ae75c2 | 38ae75c2 |") || !strings.Contains(md, `"wing-rack"`) {
				t.Fatalf("report:\n%s", md)
			}
			if _, err := CSV(nodes); err != nil {
				t.Fatal(err)
			}
			if w := sim.Writes(); len(w) != 0 {
				t.Fatalf("introspection wrote: %+v", w)
			}
		})
	}
}

// TestRequests_OnlyReadOnlyTokens: every byte the client can send decodes to
// DA, D7 <hash>, DD or DC (never a value, D8 or D9).
func TestRequests_OnlyReadOnlyTokens(t *testing.T) {
	for _, r := range []request{{root: true, defs: true}, {hash: 0xDF6A06E2, defs: true}, {hash: 0x38AE75C2}} {
		b := r.encode()
		if b[len(b)-1] != 0xDD && b[len(b)-1] != 0xDC {
			t.Fatalf("% X does not end with DD or DC", b)
		}
		if !r.root && b[0] != 0xD7 || r.root && (len(b) != 2 || b[0] != 0xDA) {
			t.Fatalf("% X", b)
		}
	}
}

// TestChildren_CappedPerCall (review sec L6): a node with more children
// than any WING has is an error, not an unbounded list.
func TestChildren_CappedPerCall(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		go func() { _, _ = server.Read(make([]byte, 64)) }()
		out := wingproto.AppendSelect(nil, wingproto.ChannelData)
		for i := range MaxChildren + 10 {
			body, _ := wingproto.AppendDefBody(nil, wingproto.Def{Hash: uint32(i + 1), Name: "n", Type: wingproto.TypeNode})
			out = wingproto.AppendEscaped(out, wingproto.AppendDefToken(nil, body))
		}
		_, _ = server.Write(out)
	}()
	defs, err := NewClient(client, 0).Children(context.Background(), true, 0)
	if !errors.Is(err, ErrTooMany) || len(defs) > MaxChildren {
		t.Fatalf("%d definitions, %v", len(defs), err)
	}
}

// TestCSV_FormulaCellsEscaped (review sec L6): cells a spreadsheet would
// run as a formula are prefixed with an apostrophe.
func TestCSV_FormulaCellsEscaped(t *testing.T) {
	b, err := CSV([]Node{{Path: "/x", Def: wingproto.Def{LongName: "=HYPERLINK(1)", Type: wingproto.TypeFader}, Value: "-10"},
		{Path: "@cmd", Def: wingproto.Def{LongName: "+1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'=HYPERLINK(1)", "'-10", "'@cmd", "'+1"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %s in\n%s", want, b)
		}
	}
}

// TestWalk_SkipsControlValues (item 14): by default the values under
// /$ctl and of action nodes are not read until hardware shows reads have
// no side effects.
func TestWalk_SkipsControlValues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sim := simwing.New(simwing.Options{})
		client, server := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { sim.ServeConn(ctx, server) })
		defer func() { cancel(); _ = client.Close(); wg.Wait() }()
		nodes, _, err := Walk(ctx, NewClient(client, 0), Options{Roots: []string{"/$ctl", "/ch/1/fdr"}, Values: true, SkipValue: DefaultSkipValue})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range sim.Log() {
			if e.Class == simwing.ClassRead && strings.HasPrefix(e.Path, "/$ctl") {
				t.Fatalf("read %s", e.Path)
			}
		}
		read := false
		for _, n := range nodes {
			read = read || (n.Path == "/ch/1/fdr" && n.Value != "")
		}
		if !read {
			t.Fatal("ordinary values must still be read")
		}
	})
}
