// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mixproto/mixcore/internal/osc"
)

// Write checks of docs/protocols/xair.md section 13. Every write goes
// through the guard (faders and sends down only, mutes on only, the name of
// one channel) and is restored at the end.

const (
	xair1Bank       = "/meters/1" // XA-F53
	xairBank1Values = 40          // XA-F53
	sendSteps       = 160         // XA-F41: 161 send steps
	echoHeadroom    = 5           // fader steps the echo check moves down
	probeName13     = "MIXPROBE-0013"
	probeName32     = "MIXPROBE-THIRTY-TWO-CHARACTERS32"
)

func xairSendAddr(ch int) string { return fmt.Sprintf("/ch/%02d/mix/01/level", ch) }

// echoRun is what each socket received after one write.
type echoRun struct{ toSub, toWriter bool }

// xairEcho runs test item 1: socket A holds /xremote, socket B (the write
// socket) never does. Writes step a fader down from the snapshot and log who
// hears about them (XA-F22, XA-F23).
func (p *prober) xairEcho(ctx context.Context) error {
	ch, snap := p.loudestFader()
	addr := chAddr(ch, "mix/fader")
	k0 := int(math.Round(float64(snap) * faderSteps))
	if k0 < echoHeadroom {
		p.add(Finding{ID: "echo-to-sender", Facts: []string{"XA-F22", "XA-F23"}, Title: "echo rules", Status: Skipped,
			Detail: "every allowed fader is at or near -oo; raise one above -60 dB and run again"})
		return nil
	}
	a, err := p.subscriber(ctx)
	if err != nil {
		return err
	}
	defer a.close()
	step := func(k int) osc.Arg { return osc.Float(float32(float64(k) / faderSteps)) }
	b, err := p.echoWrite(nil, a, addr, step(k0-1)) // (a) B writes
	if err != nil {
		return err
	}
	own, err := p.echoWrite(a, a, addr, step(k0-2)) // (b) A writes
	if err != nil {
		return err
	}
	if err := a.send("/xremotenfb"); err != nil { // (c) the no-feedback variant
		return err
	}
	_, _ = a.collect(replyWait)
	bNFB, err := p.echoWrite(nil, a, addr, step(k0-3))
	if err != nil {
		return err
	}
	ownNFB, err := p.echoWrite(a, a, addr, step(k0-4))
	if err != nil {
		return err
	}
	same, err := p.echoWrite(nil, a, addr, step(k0-4)) // (d) the value it already has
	if err != nil {
		return err
	}
	p.addEchoFindings(addr, b, own, bNFB, ownNFB, same)
	return nil
}

// echoWrite writes v to addr from writer (nil: the write socket) and reports
// whether the subscriber and the writing socket received anything within pushWait.
func (p *prober) echoWrite(writer, sub *rawClient, addr string, v osc.Arg) (echoRun, error) {
	if err := p.guard.check(addr, v); err != nil {
		return echoRun{}, err
	}
	w := writer
	if w == nil {
		w = p.w
		p.w.drain()
	}
	if err := w.send(addr, v); err != nil {
		return echoRun{}, err
	}
	var r echoRun
	_, r.toSub, _ = sub.waitFor(addr, pushWait)
	if w != sub {
		_, r.toWriter, _ = w.waitFor(addr, replyWait)
	}
	return r, nil
}

func (p *prober) addEchoFindings(addr string, b, own, bNFB, ownNFB, same echoRun) {
	ev := map[string]string{
		"(a) write socket writes: /xremote socket got it": fmt.Sprint(b.toSub), "(a) write socket got an answer": fmt.Sprint(b.toWriter),
		"(b) /xremote socket writes: got it back": fmt.Sprint(own.toSub),
		"(c) /xremotenfb: other's write pushed":   fmt.Sprint(bNFB.toSub), "(c) /xremotenfb: own write back": fmt.Sprint(ownNFB.toSub),
		"(d) unchanged value pushed": fmt.Sprint(same.toSub),
	}
	push := Finding{ID: "echo-to-sender", Facts: []string{"XA-F23"}, Title: "with /xremote the mixer pushes a change back to its sender (H2)",
		Status: Confirmed, Detail: addr + ": the /xremote socket received its own write back", Evidence: ev}
	if !own.toSub {
		push.Status, push.Detail = Refuted, addr+": no push back to the sender (H1, as X32); update xair.md and the sim default"
	}
	reply := Finding{ID: "reply-to-set", Facts: []string{"XA-F22"}, Title: "a set is answered to its sender (H3)", Status: Confirmed,
		Detail: "the write socket (no /xremote) received an answer to its set"}
	if !b.toWriter {
		reply.Status, reply.Detail = Refuted, "the write socket received nothing after its set"
	}
	nfb := Finding{ID: "xremotenfb", Facts: []string{"XA-F23"}, Title: "/xremotenfb: others' changes pushed, own changes not",
		Status: Measured, Detail: fmt.Sprintf("other's write pushed %t, own write back %t; mixcore uses /xremote", bNFB.toSub, ownNFB.toSub)}
	p.add(push)
	p.add(reply)
	p.add(nfb)
	p.add(Finding{ID: "unchanged-write", Facts: []string{"X32-F24"}, Title: "a write of the value the mixer already has is pushed",
		Status: Measured, Detail: fmt.Sprintf("pushed = %t", same.toSub)})
}

// xairClientLimit runs test item 2: nine /xremote sockets, a change from the
// write socket, who gets it, and what the ninth gets for a read, /status and
// a meter request (XA-F16, XA-F21). The change is a mute, undone at once.
func (p *prober) xairClientLimit(ctx context.Context) error {
	ch, ok := p.firstUnmuted()
	if !ok {
		p.add(Finding{ID: "push-clients", Facts: []string{"XA-F21"}, Title: "eight push clients", Status: Skipped, Detail: "every allowed channel is muted"})
		return nil
	}
	p.say("Waiting %s for the mixer to forget the /xremote sockets of the earlier checks ...", slotExpiry)
	if !sleepCtx(ctx, slotExpiry) {
		return errAborted
	}
	var subs []*rawClient
	defer func() {
		for _, s := range subs {
			s.close()
		}
	}()
	for range p.cfg.fam.pushClients + 1 {
		s, err := p.subscriber(ctx)
		if err != nil {
			return err
		}
		subs = append(subs, s)
	}
	addr := chAddr(ch, "mix/on")
	if err := p.guard.write(addr, osc.Int(0)); err != nil {
		return err
	}
	served := make([]bool, len(subs))
	for i, s := range subs {
		_, served[i], _ = s.waitFor(addr, pushWait)
	}
	if err := p.guard.write(addr, osc.Int(1)); err != nil { // back to the snapshot value
		return err
	}
	p.add(pushClientsFinding(served))
	return p.ninthClientFinding(subs[0], subs[len(subs)-1], addr)
}

func pushClientsFinding(served []bool) Finding {
	n := len(served)
	f := Finding{ID: "push-clients", Facts: []string{"XA-F20", "XA-F21"}, Title: "eight /xremote clients get pushes, the ninth does not",
		Detail: fmt.Sprintf("served %v", served)}
	switch {
	case allTrue(served[:n-1]) && !served[n-1]:
		f.Status = Confirmed
	case !served[0] && served[n-1]:
		f.Status, f.Detail = Refuted, f.Detail+": the ninth evicted the oldest"
	case allTrue(served):
		f.Status, f.Detail = Refuted, f.Detail+": more than eight are served"
	default:
		f.Status, f.Detail = Refuted, f.Detail+": fewer than eight served (another app connected?)"
	}
	return f
}

// ninthClientFinding compares a served socket with the ninth.
func (p *prober) ninthClientFinding(served, ninth *rawClient, addr string) error {
	_, readOK, err := ninth.read(addr, replyWait)
	if err != nil {
		return err
	}
	statusOf := func(c *rawClient) string {
		c.drain()
		if c.send("/status") != nil {
			return "send failed"
		}
		if m, ok, _ := c.waitFor("/status", replyWait); ok && len(m.Strings) > 0 {
			return m.Strings[0]
		}
		return "no reply"
	}
	sServed, sNinth := statusOf(served), statusOf(ninth)
	_ = ninth.send("/meters", osc.String(xair1Bank), osc.Int(0))
	_, metersOK, _ := ninth.waitFor(xair1Bank, pushWait)
	f := Finding{ID: "ninth-client", Facts: []string{"XA-F16", "XA-F21"}, Title: "what the ninth client gets; /status as a limit signal",
		Status: Measured, Detail: fmt.Sprintf("read answered %t, meters %t; /status served %q, ninth %q", readOK, metersOK, sServed, sNinth)}
	if sNinth == "standby" && sServed == "active" {
		f.Detail += ": /status signals the limit (add it to the driver's evidence, xair.md section 5)"
	}
	p.add(f)
	return nil
}

// xairSendSteps runs test item 8: a send level between two steps, below the
// snapshot, is stored on one of 161 steps (XA-F41).
func (p *prober) xairSendSteps(context.Context) error {
	addr := xairSendAddr(p.cfg.channels[0])
	snap, _ := p.guard.value(addr)
	k0 := int(math.Round(float64(snap.Float) * sendSteps))
	if k0 < 2 {
		p.add(Finding{ID: "send-steps", Facts: []string{"XA-F41"}, Title: "161 send steps", Status: Skipped,
			Detail: addr + " is at or near -oo; raise it and run again"})
		return nil
	}
	probe := float32((float64(k0-1) - 0.3) / sendSteps)
	if err := p.guard.write(addr, osc.Float(probe)); err != nil {
		return err
	}
	back, ok, err := p.w.read(addr, replyWait)
	if err != nil {
		return err
	}
	if err := p.guard.write(addr, snap); err != nil {
		return err
	}
	f := Finding{ID: "send-steps", Facts: []string{"XA-F41"}, Title: "send levels are stored on 161 steps",
		Evidence: map[string]string{"wrote": fmt.Sprintf("%.7f", probe), "readBack": argString(back)}}
	got := float64(back.Float) * sendSteps
	switch {
	case !ok:
		f.Status, f.Detail = Inconclusive, "no read-back"
	case math.Abs(got-float64(k0-1)) < 1e-3:
		f.Status, f.Detail = Confirmed, fmt.Sprintf("stored as step %d of 160", k0-1)
	default:
		f.Status, f.Detail = Refuted, fmt.Sprintf("stored as %.4f steps of 160, not on a step", got)
	}
	p.add(f)
	return nil
}

// xairNameLength runs test item 14 on the last allowed channel: a 13- and a
// 32-character name are written and read back, then the snapshot name.
func (p *prober) xairNameLength(context.Context) error {
	addr := chAddr(p.cfg.channels[len(p.cfg.channels)-1], "config/name")
	snap, _ := p.guard.value(addr)
	var parts []string
	for _, name := range []string{probeName13, probeName32} {
		if err := p.guard.write(addr, osc.String(name)); err != nil {
			return err
		}
		back, ok, err := p.w.read(addr, replyWait)
		if err != nil {
			return err
		}
		parts = append(parts, fmt.Sprintf("%d chars -> %s (answered %t)", len(name), argString(back), ok))
	}
	if err := p.guard.write(addr, snap); err != nil {
		return err
	}
	p.add(Finding{ID: "name-length", Facts: []string{"XA section 3.2"}, Title: "longest stored channel name (mixcore keeps 12)",
		Status: Measured, Detail: addr + ": " + strings.Join(parts, "; ")})
	return nil
}

// xairManualItems records the section 13 items that need a person, hardware
// changes or a write mixprobe cannot bound, so the report lists them.
func (p *prober) xairManualItems(context.Context) error {
	for _, it := range []struct{ id, facts, why string }{
		{"ap-mode", "XA-F80", "item 4: needs the REMOTE switch and five Wi-Fi devices"},
		{"meter-values", "XA-F52", "item 6: needs a 1 kHz tone at -18 dBFS and an ear on the EQ"},
		{"pan-one", "XA-F101", "item 7, pan part: writing pan 1.0 moves the image; mixprobe only lowers levels"},
		{"send-link", "XA section 3.3", "item 10, link part: needs bus links and tap changes in X AIR Edit"},
		{"xair-edit-capture", "XA-F94", "item 17: needs a mirrored switch port and X AIR Edit"},
		{"bad-writes", "XA-F03", "item 18, write part: a wrong type or 1.5 on pan could raise the level; mixprobe never sends a write it cannot bound"},
		{"mute-semantics", "XA section 3.2", "item 19: dcamute/hardmute need a person listening"},
	} {
		p.add(Finding{ID: it.id, Facts: []string{it.facts}, Title: "manual check", Status: Skipped, Detail: it.why})
	}
	return nil
}
