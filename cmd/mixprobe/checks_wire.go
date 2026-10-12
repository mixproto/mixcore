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

const (
	faderSteps   = 1023 // X32-F41
	quarterStep  = 256  // nearest step of 0.25 (X32-F41); the PDF formula gives 255
	unknownAddr  = "/ch/01/mix/mixprobe"
	xinfoStrings = 4 // X32-F10
)

// identify reads /xinfo and /info (X32-F10, X32-F11).
func (p *prober) identify() error {
	if err := p.w.send("/xinfo"); err != nil {
		return err
	}
	m, ok, err := p.w.waitFor("/xinfo", 2*replyWait)
	if err != nil {
		return err
	}
	if !ok || len(m.Strings) < xinfoStrings {
		return fmt.Errorf("%w: no /xinfo reply from %s (is the IP right and the console on?)", errAborted, p.cfg.console)
	}
	for i, k := range []string{"ip", "name", "model", "firmware"} {
		p.rep.Console[k] = m.Strings[i]
	}
	detail := fmt.Sprintf("%s %s firmware %s at %s", m.Strings[2], m.Strings[1], m.Strings[3], m.Strings[0])
	if err := p.w.send("/info"); err == nil {
		if im, ok, _ := p.w.waitFor("/info", replyWait); ok {
			p.rep.Console["info"] = strings.Join(im.Strings, " ")
		}
	}
	p.add(Finding{ID: "identify", Facts: []string{"X32-F10", "X32-F11"}, Title: "console identity", Status: Confirmed, Detail: detail,
		Evidence: map[string]string{"/xinfo": strings.Join(m.Strings, " | "), "/info": p.rep.Console["info"]}})
	return nil
}

// inventoryItem is one address the driver reads (or plans to read) at connect.
type inventoryItem struct {
	facts string
	addr  string
	typ   osc.Type
}

var inventory = []inventoryItem{
	{"X32-F31", "/ch/01/config/name", osc.TypeString}, {"X32-F32", "/ch/01/mix/fader", osc.TypeFloat},
	{"X32-F33", "/ch/01/mix/on", osc.TypeInt}, {"X32-F34", "/main/st/mix/fader", osc.TypeFloat},
	{"X32-F35", "/config/mute/1", osc.TypeInt}, {"X32-F35", "/config/mute/6", osc.TypeInt},
	{"X32-F36", "/ch/01/grp/mute", osc.TypeInt}, {"X32-F36", "/ch/32/grp/mute", osc.TypeInt},
	{"X32-F36", "/main/st/grp/mute", osc.TypeInt},
	{"X32-F37", "/dca/1/on", osc.TypeInt}, {"X32-F37", "/dca/8/on", osc.TypeInt},
	{"X32-F38", "/ch/01/grp/dca", osc.TypeInt}, {"X32-F38", "/ch/32/grp/dca", osc.TypeInt},
	{"X32-F61", "/-show/prepos/current", osc.TypeInt}, {"X32-F62", "/-undo/time", osc.TypeString},
}

// checkInventory reads every address the driver relies on: a missing one
// would keep the driver from connecting (X32-F04).
func (p *prober) checkInventory(context.Context) error {
	byFact := map[string][]string{}
	failed := map[string]bool{}
	var order []string
	for _, it := range inventory {
		if _, seen := byFact[it.facts]; !seen {
			order = append(order, it.facts)
		}
		a, ok, err := p.w.read(it.addr, replyWait)
		if err != nil {
			return err
		}
		res := "no reply"
		if ok {
			res = argString(a)
		}
		if !ok || a.Type != it.typ {
			failed[it.facts] = true
			res += fmt.Sprintf(" (want type %c)", it.typ)
		}
		byFact[it.facts] = append(byFact[it.facts], it.addr+" = "+res)
	}
	for _, fact := range order {
		f := Finding{ID: "read-" + fact, Facts: []string{fact, "X32-F04"}, Title: "addresses answer reads with the documented type",
			Status: Confirmed, Detail: strings.Join(byFact[fact], "; ")}
		if failed[fact] {
			f.Status = Refuted
		}
		p.add(f)
	}
	_, ok, err := p.w.read(unknownAddr, replyWait)
	if err != nil {
		return err
	}
	f := Finding{ID: "unknown-address", Facts: []string{"X32-F06"}, Title: "an unknown address gets no reply", Status: Confirmed,
		Detail: "read of " + unknownAddr + " got no reply"}
	if ok {
		f.Status, f.Detail = Refuted, "read of "+unknownAddr+" was answered"
	}
	p.add(f)
	return nil
}

// checkWriteSocketReads: the snapshot was read from the write socket, which
// never sent /xremote (to-verify item 9: the background re-read relies on it).
func (p *prober) checkWriteSocketReads(context.Context) error {
	p.add(Finding{ID: "write-socket-reads", Facts: []string{"X32-F01", "X32-F21"}, Title: "reads from a socket without /xremote are answered to it",
		Status: Confirmed, Detail: fmt.Sprintf("all %d snapshot reads were answered on the write socket", len(p.guard.params))})
	return nil
}

// firstUnmuted returns an allowed channel that is on (not muted) in the snapshot.
func (p *prober) firstUnmuted() (int, bool) {
	for _, ch := range p.cfg.channels {
		if a, ok := p.guard.value(chAddr(ch, "mix/on")); ok && a.Int == 1 {
			return ch, true
		}
	}
	return 0, false
}

// subscriber opens a socket that holds /xremote and drains what it gets first.
func (p *prober) subscriber(ctx context.Context) (*rawClient, error) {
	s, err := openRaw(ctx, p.cfg.listen, p.cfg.console)
	if err != nil {
		return nil, err
	}
	if err := s.send("/xremote"); err != nil {
		s.close()
		return nil, err
	}
	_, _ = s.collect(replyWait)
	return s, nil
}

// checkPushAndEcho mutes one channel from the write socket and unmutes it
// from the /xremote socket (X32-F22, X32-F23, X32-F24).
func (p *prober) checkPushAndEcho(ctx context.Context) error {
	ch, ok := p.firstUnmuted()
	if !ok {
		p.add(Finding{ID: "push-and-echo", Facts: []string{"X32-F22", "X32-F23", "X32-F24"}, Title: "push and echo",
			Status: Skipped, Detail: "every allowed channel is muted; unmute one of them on the console and run again"})
		return nil
	}
	s, err := p.subscriber(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	addr := chAddr(ch, "mix/on")
	if err := p.guard.write(addr, osc.Int(0)); err != nil {
		return err
	}
	push, got, err := s.waitFor(addr, pushWait)
	if err != nil {
		return err
	}
	p.add(pushFinding(addr, push, got))
	if err := p.guard.write(addr, osc.Int(0)); err != nil { // the same value again
		return err
	}
	_, again, err := s.waitFor(addr, pushWait)
	if err != nil {
		return err
	}
	p.add(Finding{ID: "unchanged-write", Facts: []string{"X32-F24"}, Title: "a write of the value the console already has is pushed",
		Status: Measured, Detail: fmt.Sprintf("second identical write of %s: pushed = %t", addr, again)})
	return p.checkEchoToSender(s, addr)
}

func pushFinding(addr string, push rxMsg, got bool) Finding {
	f := Finding{ID: "push-to-xremote", Facts: []string{"X32-F22", "X32-F23"}, Title: "a write from another socket is pushed to the /xremote socket",
		Status: Confirmed, Detail: "muted " + addr + " from the write socket; the /xremote socket received the push"}
	if !got {
		f.Status, f.Detail = Refuted, "no push of "+addr+" within "+ms(pushWait)
		return f
	}
	f.Evidence = map[string]string{"push": push.Address + " " + argString(push.Arg), "at": stamp(push.At)}
	return f
}

// checkEchoToSender unmutes the channel from the /xremote socket itself: the
// console must not push it back to that socket (X32-F23).
func (p *prober) checkEchoToSender(s *rawClient, addr string) error {
	if err := p.guard.check(addr, osc.Int(1)); err != nil {
		return err
	}
	if err := s.send(addr, osc.Int(1)); err != nil {
		return err
	}
	_, echoed, err := s.waitFor(addr, pushWait)
	if err != nil {
		return err
	}
	back, ok, err := p.w.read(addr, replyWait)
	if err != nil {
		return err
	}
	f := Finding{ID: "no-echo-to-sender", Facts: []string{"X32-F23"}, Title: "the console does not push a change back to its sender",
		Status: Confirmed, Detail: "unmuted " + addr + " from the /xremote socket; nothing came back to it"}
	switch {
	case !ok || back.Int != 1:
		f.Status, f.Detail = Inconclusive, "the write from the /xremote socket was not applied (read back "+argString(back)+")"
	case echoed:
		f.Status, f.Detail = Refuted, "the /xremote socket received its own write back"
	}
	p.add(f)
	return nil
}

// checkQuantization writes a fader value between two steps (downwards only)
// and reads back where the console put it (X32-F41).
func (p *prober) checkQuantization(context.Context) error {
	ch, snap := p.loudestFader()
	addr := chAddr(ch, "mix/fader")
	k0 := int(math.Round(float64(snap) * faderSteps))
	var probe float32
	var want int
	switch {
	case snap >= 0.25:
		probe, want = 0.25, quarterStep
	case k0 >= 2:
		probe, want = float32((float64(k0-1)-0.3)/faderSteps), k0-1
	default:
		p.add(Finding{ID: "fader-steps", Facts: []string{"X32-F41", "X32-F42"}, Title: "1024 fader steps, nearest rounding",
			Status: Skipped, Detail: "every allowed fader is at or near -oo; raise one above -60 dB on the console and run again"})
		return nil
	}
	if err := p.guard.write(addr, osc.Float(probe)); err != nil {
		return err
	}
	back, ok, err := p.w.read(addr, replyWait)
	if err != nil {
		return err
	}
	if err := p.guard.write(addr, osc.Float(snap)); err != nil { // straight back
		return err
	}
	p.add(quantFinding(addr, probe, want, back, ok))
	return nil
}

func quantFinding(addr string, probe float32, want int, back osc.Arg, ok bool) Finding {
	f := Finding{ID: "fader-steps", Facts: []string{"X32-F41"}, Title: "the console rounds a fader value to the nearest of 1024 steps",
		Evidence: map[string]string{"address": addr, "wrote": fmt.Sprintf("%.7f", probe)}}
	if !ok {
		f.Status, f.Detail = Inconclusive, "no read-back"
		return f
	}
	got := int(math.Round(float64(back.Float) * faderSteps))
	f.Evidence["readBack"] = fmt.Sprintf("%.7f = step %d", back.Float, got)
	switch got {
	case want:
		f.Status, f.Detail = Confirmed, fmt.Sprintf("stored as step %d (nearest)", got)
	case want - 1:
		f.Status, f.Detail = Refuted, fmt.Sprintf("stored as step %d, one below the nearest: the PDF formula int(f*1023.5)/1023 applies", got)
	default:
		f.Status, f.Detail = Refuted, fmt.Sprintf("stored as step %d, expected %d", got, want)
	}
	return f
}

// loudestFader returns the allowed channel with the highest fader snapshot.
func (p *prober) loudestFader() (int, float32) {
	best, level := p.cfg.channels[0], float32(-1)
	for _, ch := range p.cfg.channels {
		if a, ok := p.guard.value(chAddr(ch, "mix/fader")); ok && a.Float > level {
			best, level = ch, a.Float
		}
	}
	return best, level
}
