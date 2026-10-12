// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package flow

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/osc"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var (
	consoleUDP = netip.MustParseAddrPort("10.0.0.1:10024")
	consoleTCP = netip.MustParseAddrPort("10.0.0.1:49280")
	appUDP     = netip.MustParseAddrPort("10.0.0.2:50000")
	appTCP     = netip.MustParseAddrPort("10.0.0.2:50001")
	t0         = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
)

type rig struct {
	t   *testing.T
	tab *Table
	n   int
	obs []Observation
}

func newRig(t *testing.T, cfg Config) *rig {
	t.Helper()
	cfg.Console = consoleUDP.Addr()
	tab, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, tab: tab}
}

func (r *rig) emit(o Observation) { r.obs = append(r.obs, o) }

func (r *rig) send(p netpkt.Packet) {
	r.t.Helper()
	raw, err := netpkt.Build(p)
	if err != nil {
		r.t.Fatal(err)
	}
	r.n++
	r.tab.Add(Frame{At: t0.Add(time.Duration(r.n) * time.Millisecond), LinkType: netpkt.LinkRaw, Data: raw,
		Evidence: Evidence{File: "capture.pcap", Offset: int64(r.n * 100), Packet: r.n}}, r.emit)
}

func (r *rig) keys() string {
	var out []string
	for _, o := range r.obs {
		out = append(out, o.Dir.String()+" "+o.Msg.Key.String()+"="+o.Msg.Value.Text)
	}
	return strings.Join(out, "\n")
}

func oscPkt(t *testing.T, addr string, args ...osc.Arg) []byte {
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUDPFlowDetectsOSCAndKeepsOrder(t *testing.T) {
	r := newRig(t, Config{DetectChunks: 3})
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: appUDP, Dst: consoleUDP, Payload: oscPkt(t, "/ch/01/mix/fader")})
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: consoleUDP, Dst: appUDP, Payload: oscPkt(t, "/ch/01/mix/fader", osc.Float(0.5))})
	if len(r.obs) != 0 {
		t.Fatal("observations before the decoder was chosen")
	}
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: appUDP, Dst: consoleUDP, Payload: oscPkt(t, "/ch/01/mix/fader", osc.Float(0.75))})
	want := "app->console /ch/01/mix/fader=\nconsole->app /ch/01/mix/fader=0.5\napp->console /ch/01/mix/fader=0.75"
	if got := r.keys(); got != want {
		t.Fatalf("observations:\n%s\nwant:\n%s", got, want)
	}
	o := r.obs[1]
	if o.App != appUDP || o.Console != consoleUDP || o.Decoder != "osc" || o.Evidence.String() != "capture.pcap:200" {
		t.Fatalf("observation context = %+v", o)
	}
	flows := r.tab.Flows()
	if len(flows) != 1 || flows[0].Messages != 3 || !strings.Contains(flows[0].String(), "osc") {
		t.Fatalf("flows = %v", flows)
	}
}

func TestForeignAndSkippedTraffic(t *testing.T) {
	r := newRig(t, Config{})
	other := netip.MustParseAddrPort("10.0.0.9:53")
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: appUDP, Dst: other, Payload: []byte("dns")})
	r.tab.Add(Frame{LinkType: netpkt.LinkEthernet, Data: make([]byte, 60)}, r.emit) // ethertype 0: skipped
	r.tab.Add(Frame{LinkType: netpkt.LinkRaw, Data: []byte{0x45}}, r.emit)
	st := r.tab.Stats()
	if st.Foreign != 1 || st.Skipped != 1 || st.Malformed != 1 || st.Frames != 3 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestPortFilterAndForcedDecoder(t *testing.T) {
	r := newRig(t, Config{Ports: []uint16{10024}, Force: map[uint16]string{10024: "binary"}})
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: appUDP, Dst: netip.MustParseAddrPort("10.0.0.1:9999"), Payload: []byte{1}})
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: appUDP, Dst: consoleUDP, Payload: oscPkt(t, "/xinfo")})
	r.tab.Flush(r.emit)
	if r.tab.Stats().Foreign != 1 || len(r.obs) != 1 || r.obs[0].Decoder != "binary" {
		t.Fatalf("stats %+v, obs %v", r.tab.Stats(), r.keys())
	}
	if _, err := New(Config{Console: consoleUDP.Addr(), Force: map[uint16]string{1: "nope"}}); err == nil {
		t.Fatal("unknown forced decoder accepted")
	}
	if _, err := New(Config{}); !errors.Is(err, ErrNoConsole) {
		t.Fatalf("err = %v, want ErrNoConsole", err)
	}
}

func TestTickDecidesAfterWindow(t *testing.T) {
	r := newRig(t, Config{DetectWindow: time.Second})
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: appUDP, Dst: consoleUDP, Payload: oscPkt(t, "/xinfo")})
	r.tab.Tick(t0.Add(500*time.Millisecond), r.emit)
	if len(r.obs) != 0 {
		t.Fatal("decided before the window ended")
	}
	r.tab.Tick(t0.Add(2*time.Second), r.emit)
	if len(r.obs) != 1 {
		t.Fatalf("not decided after the window: %d observations", len(r.obs))
	}
}

func tcp(src, dst netip.AddrPort, seq uint32, flags uint8, data string) netpkt.Packet {
	return netpkt.Packet{Transport: netpkt.TCP, Src: src, Dst: dst, Seq: seq, Flags: flags, Payload: []byte(data)}
}

func TestTCPReassemblyOrdersRetransmitsAndGaps(t *testing.T) {
	r := newRig(t, Config{DetectChunks: 1})
	ack := netpkt.FlagACK | netpkt.FlagPSH
	r.send(tcp(appTCP, consoleTCP, 99, netpkt.FlagSYN, ""))
	r.send(tcp(appTCP, consoleTCP, 100, ack, "set A/b 0 0 1\nset A/b"))
	r.send(tcp(appTCP, consoleTCP, 128, ack, "set A/b 0 0 3\n")) // arrives early
	r.send(tcp(appTCP, consoleTCP, 121, ack, " 0 0 2\n"))        // fills the hole
	r.send(tcp(appTCP, consoleTCP, 121, ack, " 0 0 2\n"))        // retransmission
	want := "app->console A/b 0 0=1\napp->console A/b 0 0=2\napp->console A/b 0 0=3"
	if got := r.keys(); got != want {
		t.Fatalf("observations:\n%s\nwant:\n%s", got, want)
	}
	r.obs = nil
	for i := range maxOutOfOrder + 1 { // a hole that never fills
		r.send(tcp(appTCP, consoleTCP, uint32(1000+i*10), ack, "set A/c 0 0 9\n"))
	}
	if r.tab.Stats().Gaps != 1 || len(r.obs) == 0 {
		t.Fatalf("gaps = %d, observations %d", r.tab.Stats().Gaps, len(r.obs))
	}
}

// FuzzTCPDir: arbitrary segment sequences never panic, never hold more than
// maxOutOfOrder segments, and never emit more bytes than they were given.
func FuzzTCPDir(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 2, 5, 'a', 'b', 'c', 'd', 'e', 0, 0, 0, 0, 0x10, 2, 'a', 'b'})
	f.Fuzz(func(t *testing.T, b []byte) {
		var d tcpDir
		in, out := 0, 0
		for len(b) >= 6 {
			seq, flags, n := binary.BigEndian.Uint32(b), b[4], int(b[5])
			b = b[6:]
			n = min(n, len(b))
			chunks, _ := d.accept(seq, flags, b[:n])
			in += n
			b = b[n:]
			for _, c := range chunks {
				out += len(c)
			}
			if len(d.ooo) > maxOutOfOrder {
				t.Fatalf("%d out-of-order segments held", len(d.ooo))
			}
		}
		if out > in {
			t.Fatalf("emitted %d bytes from %d", out, in)
		}
	})
}

func TestSameHostAppToldApartByConsolePort(t *testing.T) {
	r := newRig(t, Config{Ports: []uint16{10024}, DetectChunks: 1})
	sameHostApp := netip.MustParseAddrPort("10.0.0.1:50000")
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: sameHostApp, Dst: consoleUDP, Payload: oscPkt(t, "/xinfo")})
	r.send(netpkt.Packet{Transport: netpkt.UDP, Src: consoleUDP, Dst: sameHostApp, Payload: oscPkt(t, "/xinfo", osc.String("x"))})
	if got := r.keys(); got != "app->console /xinfo=\nconsole->app /xinfo=\"x\"" {
		t.Fatalf("observations:\n%s", got)
	}
}
