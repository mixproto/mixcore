// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mixproto/mixcore/api"
)

const (
	eventWait     = 2 * time.Second
	sceneWindow   = 12 * time.Second // operator has 10 s to recall after Enter
	burstWindow   = 250 * time.Millisecond
	staleTarget   = 1500 * time.Millisecond // SR-05, X32-F25
	staleSlack    = 200 * time.Millisecond
	unplugWait    = 10 * time.Second
	replugWait    = 30 * time.Second
	rebootWait    = 3 * time.Minute
	undoAddr      = "/-undo/time"
	preposAddr    = "/-show/prepos/current"
	eventExternal = "externalChangeOnHeld"
	eventScene    = "sceneRecalledAtConsole"
)

// checkDeskMove holds a fader in the engine while the operator moves it at
// the desk: the push must arrive and end the gesture (X32-F22, SR-15).
func (p *prober) checkDeskMove(ctx context.Context, eng *api.Engine, rec *engineRec) error {
	ch := p.cfg.channels[0]
	if err := eng.Apply(gestureCmd("holdBegin", ch, "fader", "")); err != nil {
		return fmt.Errorf("mixprobe: hold: %w", err)
	}
	defer func() { _ = eng.Apply(gestureCmd("holdEnd", ch, "fader", "")) }()
	t0 := time.Now()
	done, err := p.cfg.prompt.Step(ctx, stepMoveFader, fmt.Sprintf("Move the fader of input %d on the console a little, then press Enter.", ch))
	if err != nil {
		return err
	}
	f := Finding{ID: "desk-move-ends-hold", Facts: []string{"X32-F22"}, Title: "a desk move is pushed and ends a held fader (SR-15)"}
	if !done {
		f.Status, f.Detail = Skipped, "operator skipped"
		p.add(f)
		return nil
	}
	p.guard.touch(chAddr(ch, "mix/fader")) // the snapshot level is put back at the end
	m, ok := rec.waitFor(ctx, t0, eventWait, func(m recMsg) bool { return len(eventsOf(m, eventExternal)) > 0 })
	switch {
	case ok:
		f.Status, f.Detail = Confirmed, fmt.Sprintf("externalChangeOnHeld after %s: %s", ms(m.at.Sub(t0)), eventsOf(m, eventExternal)[0])
	case hasAny(rec.since(t0), func(m recMsg) bool { return hasChange(m, ch, "fader", "console") }):
		f.Status, f.Detail = Refuted, "the push arrived but did not end the held gesture"
	default:
		f.Status, f.Detail = Refuted, "no push of the fader move reached the engine"
	}
	p.add(f)
	return nil
}

func hasAny(ms []recMsg, match func(recMsg) bool) bool {
	for _, m := range ms {
		if match(m) {
			return true
		}
	}
	return false
}

// checkSceneRecall records everything a desk scene recall pushes (X32-F60..F62)
// and what the engine made of it.
func (p *prober) checkSceneRecall(ctx context.Context, eng *api.Engine, rec *engineRec) error {
	ch := p.cfg.channels[0]
	s, err := p.subscriber(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	if err := eng.Apply(gestureCmd("holdBegin", ch, "fader", "")); err != nil {
		return fmt.Errorf("mixprobe: hold: %w", err)
	}
	defer func() { _ = eng.Apply(gestureCmd("holdEnd", ch, "fader", "")) }()
	done, err := p.cfg.prompt.Step(ctx, stepRecallScene,
		"Press Enter, then within 10 s recall any scene on the console (this changes the console; amplifiers must be off).")
	if err != nil || !done {
		p.add(Finding{ID: "scene-recall", Facts: []string{"X32-F60"}, Title: "desk scene recall", Status: Skipped, Detail: "operator skipped"})
		return err
	}
	t0 := time.Now()
	msgs, err := collectRenewing(s, sceneWindow)
	if err != nil {
		return err
	}
	p.guard.touchAll() // a recall may have changed every allowed parameter
	p.add(sceneFinding(msgs))
	p.add(sceneEngineFinding(rec.since(t0), ch))
	return nil
}

// collectRenewing collects for d and renews /xremote halfway (10 s lifetime).
func collectRenewing(s *rawClient, d time.Duration) ([]rxMsg, error) {
	first, err := s.collect(d / 2)
	if err != nil {
		return nil, err
	}
	if err := s.send("/xremote"); err != nil {
		return nil, err
	}
	rest, err := s.collect(d - d/2)
	return append(first, rest...), err
}

func sceneFinding(msgs []rxMsg) Finding {
	f := Finding{ID: "scene-recall-pushes", Facts: []string{"X32-F60", "X32-F61", "X32-F62"},
		Title: "what a desk scene recall pushes", Status: Measured, Evidence: map[string]string{}}
	distinct := map[string]bool{}
	for _, m := range msgs {
		distinct[m.Address] = true
		switch m.Address {
		case undoAddr, preposAddr:
			f.Evidence[m.Address+" pushed"] = argString(m.Arg) + " at " + stamp(m.At)
		}
	}
	if len(msgs) == 0 {
		f.Status, f.Detail = Inconclusive, "nothing was pushed in the window (was a scene recalled?)"
		return f
	}
	span := msgs[len(msgs)-1].At.Sub(msgs[0].At)
	f.Detail = fmt.Sprintf("%d pushes, %d distinct addresses over %s, at most %d distinct addresses within %s; /-undo/time pushed: %t; /-show/prepos/current pushed: %t",
		len(msgs), len(distinct), ms(span), maxDistinctIn(msgs, burstWindow), ms(burstWindow), f.Evidence[undoAddr+" pushed"] != "", f.Evidence[preposAddr+" pushed"] != "")
	return f
}

// maxDistinctIn is the largest number of distinct addresses within any window w.
func maxDistinctIn(msgs []rxMsg, w time.Duration) int {
	best := 0
	for i := range msgs {
		seen := map[string]bool{}
		for j := i; j < len(msgs) && msgs[j].At.Sub(msgs[i].At) <= w; j++ {
			seen[msgs[j].Address] = true
		}
		best = max(best, len(seen))
	}
	return best
}

func sceneEngineFinding(ms []recMsg, ch int) Finding {
	f := Finding{ID: "scene-recall-engine", Facts: []string{"X32-F60"}, Title: "the engine reports sceneRecalledAtConsole and ends the held fader",
		Evidence: map[string]string{}}
	var scene, ended []string
	for _, m := range ms {
		for _, raw := range eventsOf(m, eventScene) {
			scene = append(scene, string(raw))
		}
		for _, raw := range eventsOf(m, eventExternal) {
			ended = append(ended, string(raw))
		}
	}
	f.Evidence["sceneRecalledAtConsole"] = strings.Join(scene, " ")
	f.Evidence["held fader ended"] = strings.Join(ended, " ")
	switch {
	case len(scene) > 0 && len(ended) > 0:
		f.Status, f.Detail = Confirmed, fmt.Sprintf("%d scene event(s); the held fader of input %d ended", len(scene), ch)
	case len(scene) > 0:
		f.Status, f.Detail = Refuted, "scene event without ending the held fader"
	default:
		f.Status, f.Detail = Refuted, "no sceneRecalledAtConsole event"
	}
	return f
}

// checkUnplug measures how long after the last packet the engine goes stale
// (SR-05: 1.5 s +- 0.2 s) and how fast it is live again after the replug.
func (p *prober) checkUnplug(ctx context.Context, eng *api.Engine, rec *engineRec) error {
	clock, stop := startClock(ctx, eng)
	defer stop()
	t0 := time.Now()
	done, err := p.cfg.prompt.Step(ctx, stepUnplug, "Unplug the console's network cable now (leave it out), then press Enter.")
	if err != nil || !done {
		p.add(Finding{ID: "stale-timing", Facts: []string{"X32-F25"}, Title: "stale after 1.5 s", Status: Skipped, Detail: "operator skipped"})
		return err
	}
	stale, ok := rec.waitFor(ctx, t0, unplugWait, statusIs("stale"))
	f := Finding{ID: "stale-timing", Facts: []string{"X32-F25"}, Title: "stale 1.5 s +- 0.2 s after the last packet (SR-05)"}
	if ok {
		lat := stale.at.Sub(clock.latest())
		f.Detail = fmt.Sprintf("last meter frame %s, stale %s: %s after the last packet", stamp(clock.latest()), stamp(stale.at), ms(lat))
		f.Status = Confirmed
		if lat < staleTarget-staleSlack || lat > staleTarget+staleSlack {
			f.Status = Refuted
		}
	} else {
		f.Status, f.Detail = Refuted, "not stale within "+ms(unplugWait)
	}
	p.add(f)
	done, err = p.cfg.prompt.Step(ctx, stepReplug, "Plug the cable back in now, then press Enter.")
	if err != nil || !done {
		return err
	}
	p.add(liveAgainFinding(ctx, "replug-live", rec, time.Now(), replugWait))
	return nil
}

func liveAgainFinding(ctx context.Context, id string, rec *engineRec, t0 time.Time, wait time.Duration) Finding {
	f := Finding{ID: id, Facts: []string{"X32-F25"}, Title: "back to live after the link returns (target 2 s)", Status: Measured}
	if m, ok := rec.waitFor(ctx, t0, wait, statusIs("live")); ok {
		f.Detail = "live " + ms(m.at.Sub(t0)) + " after Enter"
	} else {
		f.Status, f.Detail = Refuted, "not live within "+ms(wait)
	}
	return f
}

func startClock(ctx context.Context, eng *api.Engine) (*meterClock, func()) {
	ctx, cancel := context.WithCancel(ctx)
	c := &meterClock{}
	var wg sync.WaitGroup
	wg.Go(func() { c.run(ctx, eng) })
	return c, func() { cancel(); wg.Wait() }
}

// checkReboot: the engine survives a power cycle and re-reads the console
// (X32-F26 cannot be told apart from the 10 s expiry: a boot takes longer).
func (p *prober) checkReboot(ctx context.Context, eng *api.Engine, rec *engineRec) error {
	before := lastStrips(rec)
	done, err := p.cfg.prompt.Step(ctx, stepReboot, "Reboot the console: switch it off, wait 5 s, switch it on. Press Enter once it is off.")
	if err != nil || !done {
		p.add(Finding{ID: "reboot", Facts: []string{"X32-F26"}, Title: "reboot", Status: Skipped, Detail: "operator skipped"})
		return err
	}
	p.say("Waiting up to %s for the console to boot ...", rebootWait)
	f := liveAgainFinding(ctx, "reboot-live", rec, time.Now(), rebootWait)
	f.Facts, f.Title = []string{"X32-F26"}, "live again after a power cycle (subscriptions renewed by the engine)"
	if f.Status == Measured {
		if err := eng.RequestSnapshot(); err == nil {
			time.Sleep(eventWait)
		}
		f.Detail += fmt.Sprintf("; %d strip value(s) differ from before the reboot (autosave rollback?)", diffStrips(before, lastStrips(rec)))
	}
	p.guard.touchAll()
	p.add(f)
	return nil
}

func lastStrips(rec *engineRec) []engStrip {
	ms := rec.since(time.Time{})
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].st.Type == "snapshot" && len(ms[i].st.Strips) > 0 {
			return ms[i].st.Strips
		}
	}
	return nil
}

func diffStrips(a, b []engStrip) int {
	n := 0
	for i := range min(len(a), len(b)) {
		if a[i].Name != b[i].Name || a[i].Mute != b[i].Mute || string(a[i].FaderDB) != string(b[i].FaderDB) {
			n++
		}
	}
	return n
}
