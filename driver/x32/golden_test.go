// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"encoding/hex"
	"flag"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/wire_trace.golden")

// tap records every packet a socket sends, in order.
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

// wireTrace taps the sockets of one driver in the order they are opened
// (subscription socket first, then the command socket).
type wireTrace struct {
	mu      sync.Mutex
	sockets []*tap
}

func (w *wireTrace) listen(inner driver.ListenPacketFunc) driver.ListenPacketFunc {
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

func (w *wireTrace) take() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][]string, len(w.sockets))
	for i, s := range w.sockets {
		out[i] = slices.Clone(s.sent)
		s.sent = s.sent[:0]
	}
	return out
}

// TestWire_GoldenTrace pins every byte the x32 driver puts on the wire: the
// ordered connect sequence and the distinct packets of a live session with
// writes, meters, a background re-read and a scene recall. Any refactor of
// the driver must leave this file unchanged (go test -run Golden -update
// rewrites it, which a review must then justify).
func TestWire_GoldenTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simx32.Options{})
		r.sim.SetInputLevels([]float32{1, 0.5})
		wt := &wireTrace{}
		opts := r.opts()
		opts.ListenPacket = wt.listen(opts.ListenPacket)
		sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		connect := wt.take()
		r.run(sess)
		goldenSession(t, r, sess)
		live := wt.take()
		got := renderTrace(connect, live)
		path := filepath.Join("testdata", "wire_trace.golden")
		if *updateGolden {
			if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path) //nolint:gosec // G304: fixed test fixture
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("wire trace changed; diff against %s:\n%s", path, firstDiff(string(want), got))
		}
	})
}

func goldenSession(t *testing.T, r *rig, sess driver.Session) {
	t.Helper()
	g := driver.Origin{Kind: driver.OriginGesture, ClientID: "golden", Seq: 1}
	name, _ := model.Text("Vox")
	for _, c := range []driver.Command{
		{Param: ref(1, model.FieldFader), Value: mustLevel(t, -10), Origin: g},
		{Param: ref(2, model.FieldMute), Value: model.Bool(true), Origin: g},
		{Param: ref(3, model.FieldName), Value: name, Origin: g},
		{Param: grp(model.StripMuteGroup, 2), Value: model.Bool(true), Origin: g},
		{Param: grp(model.StripDCA, 1), Value: model.Bool(true), Origin: g},
		{Param: model.ParamRef{Strip: model.MainRef, Field: model.FieldFader}, Value: mustLevel(t, 0), Origin: g},
	} {
		if err := sess.Apply(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.SetMeters(driver.MeterRequest{Inputs: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if err := sess.(driver.Rereader).Reread(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(14 * time.Second)
	if err := r.sim.RecallScene(1, faderScene(10)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	_ = sess.SetMeters(driver.MeterRequest{})
	synctest.Wait()
}

// renderTrace writes the connect sequence of each socket in order and the
// distinct packets of the live phase sorted, one hex packet per line with
// its decoded form for review.
func renderTrace(connect, live [][]string) string {
	var b strings.Builder
	for i, pkts := range connect {
		b.WriteString("## connect, socket " + sockName(i) + "\n")
		for _, p := range pkts {
			b.WriteString(describePacket(p))
		}
	}
	for i, pkts := range live {
		b.WriteString("## live distinct, socket " + sockName(i) + "\n")
		set := slices.Clone(pkts)
		slices.Sort(set)
		for _, p := range slices.Compact(set) {
			b.WriteString(describePacket(p))
		}
	}
	return b.String()
}

func sockName(i int) string { return [...]string{"subscription", "command"}[min(i, 1)] }

func describePacket(h string) string {
	raw, _ := hex.DecodeString(h)
	var m osc.Message
	text := "undecodable"
	if osc.Decode(raw, &m) == nil {
		text = m.String()
	}
	return h + "  " + text + "\n"
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
