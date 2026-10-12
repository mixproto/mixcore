// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mixproto/mixcore/driver/xair"
	"github.com/mixproto/mixcore/internal/osc"
)

// Read-only checks of docs/protocols/xair.md section 13. Nothing here writes.

const (
	reuseRounds  = 20 // test item 16
	reuseWait    = 300 * time.Millisecond
	reuseLongGap = 15 * time.Second // test item 16: also after 15 s
	snapMax      = 64               // XA-F60
)

// xairPhases: read-only checks first, then the guarded writes (each
// restored), then the engine and the operator's physical steps.
func (p *prober) xairPhases() []func(context.Context) error {
	return []func(context.Context) error{
		p.closeApps,
		p.xairIdentity, p.xairInventory, p.checkWriteSocketReads, p.xairMeters, p.xairSocketReuse,
		p.xairEcho, p.checkQuantization, p.xairSendSteps, p.xairNameLength, p.checkMuteGroup, p.xairClientLimit,
		p.xairManualItems,
		p.engineChecks,
	}
}

// xairIdentity records /status next to /xinfo and /info (test item 11, XA-F16, XA-F70).
func (p *prober) xairIdentity(context.Context) error {
	if err := p.w.send("/status"); err != nil {
		return err
	}
	m, ok, err := p.w.waitFor("/status", replyWait)
	if err != nil {
		return err
	}
	status := strings.Join(m.Strings, " | ")
	model := p.rep.Console["model"]
	f := Finding{ID: "model-strings", Facts: []string{"XA-F70", "XA-F10", "XA-F16"}, Title: "model, firmware and status strings",
		Status: Confirmed, Detail: fmt.Sprintf("model %q firmware %q; /status %q", model, p.rep.Console["firmware"], status),
		Evidence: map[string]string{"/info": p.rep.Console["info"], "/status": status}}
	if !xair.IsModel(model) || !ok {
		f.Status = Refuted
		f.Detail += ": the model does not start with XR, X18 or MR, or /status got no reply"
	}
	p.add(f)
	return nil
}

// readSet reads addrs and returns which answered, with their values.
func (p *prober) readSet(addrs []string) (answered map[string]string, err error) {
	answered = map[string]string{}
	for _, a := range addrs {
		v, ok, err := p.w.read(a, replyWait)
		if err != nil {
			return nil, err
		}
		if ok {
			answered[a] = argString(v)
		}
	}
	return answered, nil
}

// readFinding: every addr in must answer and every addr in silent must not.
func (p *prober) readFinding(id, title string, facts []string, must, silent, measure []string) error {
	got, err := p.readSet(append(append(append([]string{}, must...), silent...), measure...))
	if err != nil {
		return err
	}
	var bad, parts []string
	for _, a := range must {
		if _, ok := got[a]; !ok {
			bad = append(bad, a+" silent")
		}
	}
	for _, a := range silent {
		if _, ok := got[a]; ok {
			bad = append(bad, a+" answered")
		}
	}
	for _, a := range append(append(append([]string{}, must...), silent...), measure...) {
		v, ok := got[a]
		if !ok {
			v = "no reply"
		}
		parts = append(parts, a+" = "+v)
	}
	f := Finding{ID: id, Facts: facts, Title: title, Status: Confirmed, Detail: strings.Join(parts, "; ")}
	if len(bad) > 0 {
		f.Status, f.Detail = Refuted, strings.Join(bad, ", ")+". "+f.Detail
	}
	p.add(f)
	return nil
}

// xairInventory reads what the driver relies on and the address questions
// of test items 9, 10, 12, 13 and 18 (reads only).
func (p *prober) xairInventory(context.Context) error {
	drv := xair.New(xair.Config{}).Addresses()
	for _, c := range []struct {
		id, title             string
		facts                 []string
		must, silent, measure []string
	}{
		{"read-driver-addresses", "every address the driver reads answers (90 reads)", []string{"XA section 3", "XA section 12"}, drv, nil, nil},
		{"group-membership-addresses", "main has no grp/mute and no grp/dca; the other strips do (test item 9)", []string{"XA section 3.2"},
			[]string{"/rtn/aux/grp/mute", "/rtn/1/grp/dca", "/fxsend/1/grp/mute"}, []string{"/lr/grp/mute", "/lr/grp/dca"}, nil},
		{"send-on-absent", "sends have no on/off (test item 10, reads)", []string{"XA section 3.3"},
			[]string{"/ch/01/mix/01/level"}, []string{"/ch/01/mix/01/on"}, []string{"/ch/01/mix/02/tap", "/ch/01/mix/02/pan", "/rtn/1/mix/07/level"}},
		{"dsp-counts", "16 inputs, 6 buses, 4 FX sends and returns answer (vendor tables, test item 12)", []string{"XA-F71", "XA-F72"},
			[]string{"/ch/16/mix/fader", "/bus/6/mix/fader", "/fxsend/4/mix/fader", "/rtn/4/mix/fader"}, nil, nil},
		{"snapshot-index-address", "/-snap/index answers; /snap/index is measured (test item 13, reads)", []string{"XA-F60", "XA-F61"},
			[]string{"/-snap/index"}, nil, []string{"/snap/index"}},
		{"unknown-address", "an address outside the tree gets no reply (test item 18, read)", []string{"XA-F03"},
			nil, []string{"/ch/17/mix/fader"}, nil},
	} {
		if err := p.readFinding(c.id, c.title, c.facts, c.must, c.silent, c.measure); err != nil {
			return err
		}
	}
	return nil
}

// xairMeters requests bank 1 in the three forms of test item 5, each on a
// fresh socket; the first one runs past the expected 10 s lifetime. mixprobe
// never sends /-local/updrate (XA-F56, XA-F57).
func (p *prober) xairMeters(ctx context.Context) error {
	start := time.Now()
	r, err := p.requestMeterForm(ctx, meterLifeWait, osc.String(xair1Bank), osc.Int(0))
	if err != nil {
		return err
	}
	f := Finding{ID: "meters-bank1", Facts: []string{"XA-F50", "XA-F51", "XA-F53", "XA-F56"},
		Title: `,si "/meters/1" 0: 40 int16 values every 50 ms for 10 s`, Evidence: map[string]string{}}
	switch {
	case len(r.packets) == 0:
		f.Status, f.Detail = Refuted, "no /meters/1 packet at all"
	default:
		life := r.packets[len(r.packets)-1].At.Sub(start)
		size := len(r.packets[0].Arg.Bytes)
		f.Detail = fmt.Sprintf("count %d, blob %d bytes, median interval %s, stream stopped %s after a single request", r.count, size, ms(r.interval), ms(life))
		f.Status = Confirmed
		if r.count != xairBank1Values || size != meterCountBytes+2*r.count || !near(r.interval, meterInterval) || !near(life, meterTTL) {
			f.Status = Refuted
		}
	}
	p.add(f)
	for _, form := range []struct {
		id   string
		args []osc.Arg
	}{
		{"meters-form-s", []osc.Arg{osc.String(xair1Bank)}},
		{"meters-form-siii", []osc.Arg{osc.String(xair1Bank), osc.Int(0), osc.Int(0), osc.Int(2)}},
	} {
		r, err := p.requestMeterForm(ctx, meterSample, form.args...)
		if err != nil {
			return err
		}
		p.add(Finding{ID: form.id, Facts: []string{"XA-F50", "XA-F56"}, Title: "another request form of bank 1", Status: Measured,
			Detail: fmt.Sprintf("%d packet(s) in %s, median interval %s", len(r.packets), meterSample, ms(r.interval))})
	}
	return nil
}

// xairSocketReuse: a probe socket answered, then closed; a new socket must
// get an /xinfo reply within 300 ms, 20 times and once after 15 s (test item 16, XA-F15).
func (p *prober) xairSocketReuse(ctx context.Context) error {
	var slow []string
	worst := time.Duration(0)
	for i := range reuseRounds + 1 {
		if i == reuseRounds && !sleepCtx(ctx, reuseLongGap) {
			return errAborted
		}
		lat, ok, err := p.reuseRound(ctx)
		if err != nil {
			return err
		}
		worst = max(worst, lat)
		if !ok {
			slow = append(slow, fmt.Sprint(i+1))
		}
	}
	f := Finding{ID: "socket-reuse", Facts: []string{"XA-F15", "XA-F99"}, Title: "a new socket after discovery is answered within 300 ms",
		Status: Confirmed, Detail: fmt.Sprintf("%d rounds (the last after %s), slowest %s", reuseRounds+1, reuseLongGap, ms(worst))}
	if len(slow) > 0 {
		f.Status, f.Detail = Refuted, "no reply within 300 ms in round(s) "+strings.Join(slow, ",")+"; "+f.Detail
	}
	p.add(f)
	return nil
}

func (p *prober) reuseRound(ctx context.Context) (time.Duration, bool, error) {
	probe, err := openRaw(ctx, p.cfg.listen, p.cfg.console)
	if err != nil {
		return 0, false, err
	}
	if err := probe.send("/xinfo"); err != nil {
		probe.close()
		return 0, false, err
	}
	_, _, err = probe.waitFor("/xinfo", replyWait)
	probe.close()
	if err != nil {
		return 0, false, err
	}
	s, err := openRaw(ctx, p.cfg.listen, p.cfg.console)
	if err != nil {
		return 0, false, err
	}
	defer s.close()
	t0 := time.Now()
	if err := s.send("/xinfo"); err != nil {
		return 0, false, err
	}
	m, ok, err := s.waitFor("/xinfo", reuseWait)
	if err != nil || !ok {
		return reuseWait, false, err
	}
	return m.At.Sub(t0), true, nil
}
