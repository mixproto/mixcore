// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package analyze

import (
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/flow"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var (
	t0      = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	app1    = netip.MustParseAddrPort("10.0.0.2:50000")
	app2    = netip.MustParseAddrPort("10.0.0.3:50000")
	console = netip.MustParseAddrPort("10.0.0.1:10024")
	fader   = decode.Key{Kind: decode.KindOSC, ID: "/ch/01/mix/fader"}
	mute    = decode.Key{Kind: decode.KindOSC, ID: "/ch/01/mix/on"}
	meter   = decode.Key{Kind: decode.KindOSC, ID: "/meters/1"}
)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func num(f float64) decode.Value {
	return decode.NumValue(f, strconv.FormatFloat(f, 'g', -1, 64))
}

func obs(ms int, app netip.AddrPort, dir flow.Dir, k decode.Key, v decode.Value) flow.Observation {
	return flow.Observation{At: at(ms), Dir: dir, App: app, Console: console, Decoder: "osc",
		Msg: decode.Message{Key: k, Value: v}, Evidence: flow.Evidence{File: "capture.pcap", Offset: int64(ms)}}
}

type recorder struct{ events []string }

func (r *recorder) notify(e Event) {
	switch ev := e.(type) {
	case CandidateFound:
		r.events = append(r.events, fmt.Sprintf("candidate %d %s", ev.Mark.ID, ev.Candidate.Change()))
	case MarkClosed:
		r.events = append(r.events, fmt.Sprintf("closed %d", ev.Mark.ID))
	case HarvestDone:
		r.events = append(r.events, fmt.Sprintf("harvest %d keys %d", ev.Harvest.ID, len(ev.Harvest.Keys)))
	}
}

func TestMarkAttributesChangesAndFlagsNoise(t *testing.T) {
	rec := &recorder{}
	a := New(Config{MarkWindow: time.Second, Notify: rec.notify})
	a.Observe(obs(0, app1, flow.FromConsole, fader, num(0.75)))
	a.Observe(obs(0, app1, flow.FromConsole, mute, num(1)))
	for ms := 0; ms <= 3000; ms += 100 { // a meter that never stops changing
		a.Observe(obs(ms+1, app1, flow.FromConsole, meter, num(float64(ms))))
		if ms == 1500 {
			a.Mark(at(1500), "ch1 fader")
		}
		if ms == 1600 {
			a.Observe(obs(1600, app1, flow.ToConsole, fader, num(0.8)))
			a.Observe(obs(1601, app1, flow.FromConsole, fader, num(0.8))) // echo: no change
			a.Observe(obs(1700, app1, flow.ToConsole, fader, num(0.85)))
		}
	}
	marks := a.Marks()
	if len(marks) != 1 || !marks[0].Closed {
		t.Fatalf("marks = %+v", marks)
	}
	cands := marks[0].Candidates
	if len(cands) != 2 || cands[0].Key != fader || cands[1].Key != meter || !cands[1].Noisy || cands[0].Noisy {
		t.Fatalf("candidates = %+v", cands)
	}
	if got := cands[0].Change(); got != "/ch/01/mix/fader 0.75→0.85" || cands[0].Changes != 2 {
		t.Fatalf("change = %q (%d changes)", got, cands[0].Changes)
	}
	want := "candidate 1 /meters/1 1500→1600\ncandidate 1 /ch/01/mix/fader 0.75→0.8\nharvest 1 keys 3\nclosed 1"
	if got := strings.Join(rec.events, "\n"); got != want {
		t.Fatalf("events:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewMarkClosesPreviousAndNewKeys(t *testing.T) {
	a := New(Config{MarkWindow: 10 * time.Second})
	a.Mark(at(0), "first")
	a.Observe(obs(10, app1, flow.FromConsole, fader, num(0.1)))
	a.Mark(at(20), "second")
	a.Observe(obs(30, app1, flow.FromConsole, fader, num(0.2)))
	a.Finish()
	m := a.Marks()
	if !m[0].Closed || len(m[0].Candidates) != 1 || m[0].Candidates[0].Change() != "/ch/01/mix/fader (new)→0.1" {
		t.Fatalf("first mark = %+v", m[0])
	}
	if len(m[1].Candidates) != 1 || m[1].Candidates[0].Change() != "/ch/01/mix/fader 0.1→0.2" {
		t.Fatalf("second mark = %+v", m[1])
	}
}

func TestParsePoint(t *testing.T) {
	tests := []struct {
		label string
		want  float64
		ok    bool
	}{
		{"ch1 fader = -10 dB", -10, true},
		{"fader=+5", 5, true},
		{"fader = -inf", math.Inf(-1), true},
		{"fader = -oo dB", math.Inf(-1), true},
		{"fader = loud", 0, false},
		{"fader up", 0, false},
		{"fader =", 0, false},
		{"fader = NaN", 0, false},
	}
	for _, tc := range tests {
		got, ok := ParsePoint(tc.label)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ParsePoint(%q) = %v, %v; want %v, %v", tc.label, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLawPoints(t *testing.T) {
	a := New(Config{MarkWindow: time.Second})
	a.Observe(obs(0, app1, flow.FromConsole, fader, num(0.75)))
	a.Mark(at(100), "fader = 0 dB") // already there: value at the mark
	a.Mark(at(2000), "fader = -10") // moved after the mark: value at the end of the window
	a.Observe(obs(2100, app1, flow.ToConsole, fader, num(0.6)))
	a.Observe(obs(2200, app1, flow.ToConsole, fader, num(0.5)))
	a.Mark(at(4000), "no point here")
	a.Finish()
	pts := a.LawPoints(fader)
	if len(pts) != 2 || pts[0] != (Point{0.75, 0, "fader = 0 dB"}) || pts[1] != (Point{0.5, -10, "fader = -10"}) {
		t.Fatalf("points = %+v", pts)
	}
	if k, ok := a.FindKey("/ch/01/mix/fader"); !ok || k != fader {
		t.Fatal("FindKey failed")
	}
}

func TestSnapshotDiff(t *testing.T) {
	a := New(Config{})
	a.Observe(obs(0, app1, flow.FromConsole, fader, num(0.75)))
	a.Observe(obs(0, app1, flow.FromConsole, mute, num(1)))
	a.Snapshot(at(10), "before")
	a.Observe(obs(20, app1, flow.FromConsole, fader, num(0.5)))
	a.Observe(obs(20, app1, flow.FromConsole, meter, num(3)))
	a.Snapshot(at(30), "after recall")
	r := a.Result()
	if len(r.Diffs) != 1 {
		t.Fatalf("diffs = %+v", r.Diffs)
	}
	var got []string
	for _, c := range r.Diffs[0].Changes {
		got = append(got, fmt.Sprintf("%s added=%t", c.Detail(), c.Added))
	}
	want := "/ch/01/mix/fader 0.75→0.5 added=false\n/meters/1 (new)→3 added=true"
	if strings.Join(got, "\n") != want {
		t.Fatalf("diff:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func TestHarvestPerNewApp(t *testing.T) {
	rec := &recorder{}
	a := New(Config{HarvestQuiet: time.Second, Notify: rec.notify})
	a.Observe(obs(0, app1, flow.ToConsole, fader, decode.Value{})) // a read: opens, no value
	a.Observe(obs(5, app1, flow.FromConsole, fader, num(0.75)))
	a.Observe(obs(6, app1, flow.FromConsole, mute, num(1)))
	a.Observe(obs(800, app1, flow.FromConsole, fader, num(0.5))) // not new
	a.Tick(at(2500))
	a.Observe(obs(3000, app2, flow.ToConsole, mute, num(0)))
	a.Finish()
	hs := a.Harvests()
	if len(hs) != 2 || !hs[0].Closed || len(hs[0].Keys) != 2 || hs[1].App != app2 || len(hs[1].Keys) != 1 {
		t.Fatalf("harvests = %+v", hs)
	}
	if v := hs[0].Keys[0]; v.Key != fader || v.Value.Text != "0.5" || !hs[0].End.Equal(at(6)) {
		t.Fatalf("first harvested key = %+v, end %s", v, hs[0].End)
	}
	if rec.events[0] != "harvest 1 keys 2" {
		t.Fatalf("events = %v", rec.events)
	}
	if r := a.Result(); r.Valueless != 1 || r.Keys[0].Dirs != "both" || r.Keys[1].Dirs != "both" {
		t.Fatalf("result = %+v", r.Keys)
	}
}

func TestBinaryChangeOffsets(t *testing.T) {
	k := decode.Key{Kind: decode.KindBinary, ID: "a5010017/12"}
	a := New(Config{})
	a.Mark(at(0), "bin")
	a.Observe(obs(1, app1, flow.FromConsole, k, decode.TextValue("00005a000000")))
	a.Observe(obs(2, app1, flow.FromConsole, k, decode.TextValue("00005f000001")))
	a.Finish()
	r := a.Result()
	if got := r.Keys[0].Varying; len(got) != 2 || got[0] != 6 || got[1] != 9 {
		t.Fatalf("varying = %v", got)
	}
	if got := describeChange(k, decode.TextValue("00005a"), decode.TextValue("00005f")); got != "bin a5010017/12 @6 5a→5f" {
		t.Fatalf("describe = %q", got)
	}
	if r.Keys[0].Labels[0] != "bin" {
		t.Fatalf("labels = %v", r.Keys[0].Labels)
	}
}

func TestDiffLeavesStreamingKeysOut(t *testing.T) {
	a := New(Config{})
	a.Observe(obs(0, app1, flow.FromConsole, fader, num(0.75)))
	a.Snapshot(at(0), "a")
	for ms := 1; ms <= 2000; ms += 50 {
		a.Observe(obs(ms, app1, flow.FromConsole, meter, num(float64(ms))))
	}
	a.Observe(obs(2001, app1, flow.FromConsole, fader, num(0.5)))
	a.Snapshot(at(2100), "b")
	d := a.Result().Diffs[0]
	if len(d.Changes) != 1 || d.Changes[0].Key != fader || len(d.Noise) != 1 || d.Noise[0].Key != meter {
		t.Fatalf("diff = %+v", d)
	}
}
