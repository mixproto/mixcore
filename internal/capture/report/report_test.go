// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package report

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/capture/analyze"
	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/law"
	"github.com/mixproto/mixcore/internal/capture/rcpinfo"
	"github.com/mixproto/mixcore/internal/capture/session"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func sampleInput(t *testing.T) Input {
	t.Helper()
	fader := decode.Key{Kind: decode.KindOSC, ID: "/ch/01/mix/fader"}
	name := decode.Key{Kind: decode.KindOSC, ID: "/ch/01/config/name"}
	app := netip.MustParseAddrPort("10.0.0.2:5000")
	o := func(ms int, k decode.Key, v decode.Value) flow.Observation {
		return flow.Observation{At: t0.Add(time.Duration(ms) * time.Millisecond), Dir: flow.FromConsole, App: app,
			Decoder: "osc", Msg: decode.Message{Key: k, Value: v}, Evidence: flow.Evidence{File: "capture.pcap", Offset: int64(ms + 24)}}
	}
	a := analyze.New(analyze.Config{})
	a.Observe(o(0, fader, decode.NumValue(0.75, "0.75")))
	a.Observe(o(0, name, decode.TextValue(`"Vox | Lead"`)))
	a.Snapshot(t0.Add(time.Second), "before")
	a.Mark(t0.Add(2*time.Second), "ch1 fader = -10 dB")
	a.Observe(o(2500, fader, decode.NumValue(0.5, "0.5")))
	a.Snapshot(t0.Add(3*time.Second), "after")
	a.Finish()
	fit, _, err := law.Fit([]law.Point{{X: 0.5, Y: -10}, {X: 0.75, Y: 0}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return Input{
		Meta:   session.Meta{Console: "10.0.0.1", Mode: "relay", Started: t0, Capture: session.FileCapture},
		Result: a.Result(),
		Flows:  []flow.FlowInfo{{Transport: "udp", App: app, Decoder: "osc", Messages: 3}},
		Stats:  flow.Stats{Frames: 3, Dropped: 2},
		Laws:   map[decode.Key]Fit{fader: {Law: fit, Points: a.LawPoints(fader)}},
	}
}

func TestWriteReport(t *testing.T) {
	d, err := session.Create(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := Write(d, sampleInput(t)); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(d.Path(), name)) //nolint:gosec // G304: a file the test just wrote
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	md := read(FileReport)
	for _, want := range []string{
		session.PrivacyNote,
		"### Mark 1: \"ch1 fader = -10 dB\" at +2.000 s",
		"| `/ch/01/mix/fader` | 0.75→0.5 | console->app | 1 | capture.pcap:2524 |",
		"linear: y = 40*x - 30",
		"### Snapshot 1 -> 2: 1 changes",
		"2 decoded messages dropped as too large or too deep",
		"| `/ch/01/mix/fader` | ch1 fader = -10 dB | console->app | 0.75, 0.5 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report.md lacks %q\n%s", want, md)
		}
	}
	if strings.ContainsAny(md, "—–") {
		t.Error("report.md contains a long dash")
	}
	if keys := read(FileKeys); !strings.Contains(keys, `/ch/01/config/name,osc,osc,,console->app,1,0,"""Vox | Lead""",`) {
		t.Errorf("keys.csv:\n%s", keys)
	}
	if h := read(FileHarvest); !strings.Contains(h, "1,10.0.0.2:5000,/ch/01/mix/fader,osc,0.75,capture.pcap:24") {
		t.Errorf("harvest.csv:\n%s", h)
	}
	if m := read(FileMarks); !strings.Contains(m, "1,ch1 fader = -10 dB,/ch/01/mix/fader,0.75,0.5,console->app,1,false") {
		t.Errorf("marks.csv:\n%s", m)
	}
}

func TestWriteIntrospection(t *testing.T) {
	d, err := session.Create(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	tables := []Table{
		{Verb: rcpinfo.VerbPrmInfo, Stopped: "console answered ERROR at index 1", Entries: []rcpinfo.Entry{{
			Table: rcpinfo.VerbPrmInfo, Address: "MIXER:Current/InCh/Fader/Level",
			Fields: []string{"40", "1", "-32768", "1000", "-32768", "dB", "integer", "any", "rw", "100"}, Raw: "OK prminfo 0 ..."}}},
		{Verb: rcpinfo.VerbMtrInfo, Stopped: "no reply at index 0"},
	}
	if err := WriteIntrospection(d, session.Meta{Console: "10.0.0.5:49280", Capture: "capture.pcap"},
		map[string]string{"productname": "TF5"}, tables); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d.Path(), FileParams))
	if err != nil || !strings.Contains(string(b), "0,MIXER:Current/InCh/Fader/Level,40,1,-32768,1000,-32768,dB,integer,any,rw,100,") {
		t.Fatalf("params.csv: %s %v", b, err)
	}
	md, err := os.ReadFile(filepath.Join(d.Path(), FileIntroMD))
	if err != nil || !strings.Contains(string(md), "- productname: TF5") || !strings.Contains(string(md), "## mtrinfo: 0 entries") {
		t.Fatalf("introspect.md: %s %v", md, err)
	}
}
