// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mixproto/mixcore/api"
	"github.com/mixproto/mixcore/internal/osc"
)

const (
	quietWindow     = 20 * time.Second // a quiet console must stay live this long (X32-F25)
	slotExpiry      = 11 * time.Second // a dropped /xremote slot is free after 10 s (X32-F20)
	reconnectCycles = 20
)

// engineChecks runs the checks that use the real mixcore engine.
func (p *prober) engineChecks(ctx context.Context) error {
	if err := p.checkQuietConsole(ctx); err != nil {
		return err
	}
	p.say("Waiting %s for the console to forget the sockets used so far ...", slotExpiry)
	if !sleepCtx(ctx, slotExpiry) {
		return errAborted
	}
	eng, rec, err := p.startEngine(ctx, true)
	if err != nil {
		return err
	}
	defer eng.Close()
	for _, check := range []func(context.Context, *api.Engine, *engineRec) error{
		p.checkReconnectSlots, p.checkDeskMove, p.checkSceneRecall, p.checkUnplug, p.checkReboot,
	} {
		if err := check(ctx, eng, rec); err != nil {
			return err
		}
	}
	return nil
}

// checkQuietConsole: with meters off and nothing changing, the engine must
// stay live: the quiet /xinfo probes keep it fresh (X32-F25).
func (p *prober) checkQuietConsole(ctx context.Context) error {
	eng, rec, err := p.startEngine(ctx, false)
	if err != nil {
		return err
	}
	defer eng.Close()
	t0 := time.Now()
	p.say("Watching a quiet console for %s (do not touch the console) ...", quietWindow)
	if !sleepCtx(ctx, quietWindow) {
		return errAborted
	}
	f := Finding{ID: "quiet-console-stays-live", Facts: []string{"X32-F25"}, Title: "a quiet console never goes stale (quiet /xinfo probes)",
		Status: Confirmed, Detail: fmt.Sprintf("meters off, %s without stale", quietWindow)}
	if m, bad := rec.waitFor(ctx, t0, 0, func(m recMsg) bool { return m.st.Status != "live" }); bad {
		f.Status, f.Detail = Refuted, fmt.Sprintf("went %s after %s: %s", m.st.Status, ms(m.at.Sub(t0)), m.raw)
	}
	p.add(f)
	return nil
}

// checkReconnectSlots reconnects 20 times over the same sockets, then fills
// the console's client table with raw /xremote clients and checks who still
// gets pushes (X32-F21, X32-F28).
func (p *prober) checkReconnectSlots(ctx context.Context, eng *api.Engine, rec *engineRec) error {
	worst, err := p.reconnectCycles(ctx, eng, rec)
	if err != nil {
		return err
	}
	p.add(Finding{ID: "reconnect-cycles", Facts: []string{"X32-F28"}, Title: "20 reconnects over the same sockets",
		Status: Measured, Detail: fmt.Sprintf("%d Pause/Resume cycles, slowest back to live in %s", reconnectCycles, ms(worst))})
	ch, ok := p.firstUnmuted()
	if !ok {
		p.add(Finding{ID: "client-slots", Facts: []string{"X32-F21", "X32-F28"}, Title: "client slots", Status: Skipped,
			Detail: "every allowed channel is muted"})
		return nil
	}
	clients, err := p.fillSlots(ctx)
	if err == nil {
		var served []bool
		if served, err = p.whoIsServed(ctx, eng, rec, ch, clients); err == nil {
			p.add(slotFinding(served, p.cfg.fam.pushClients))
		}
	}
	for _, c := range clients {
		c.close()
	}
	if err != nil {
		return err
	}
	p.say("Waiting %s for the console to free the slots of the extra clients ...", slotExpiry)
	if !sleepCtx(ctx, slotExpiry) {
		return errAborted
	}
	return nil
}

func (p *prober) reconnectCycles(ctx context.Context, eng *api.Engine, rec *engineRec) (time.Duration, error) {
	var worst time.Duration
	for i := range reconnectCycles {
		if err := eng.Pause(); err != nil {
			return 0, fmt.Errorf("mixprobe: pause: %w", err)
		}
		t0 := time.Now()
		if err := eng.Resume(); err != nil {
			return 0, fmt.Errorf("mixprobe: resume: %w", err)
		}
		m, ok := rec.waitFor(ctx, t0, liveWait, statusIs("live"))
		if !ok {
			return 0, fmt.Errorf("%w: reconnect %d did not get live", errAborted, i+1)
		}
		worst = max(worst, m.at.Sub(t0))
	}
	return worst, nil
}

// fillSlots opens raw clients that each send /xremote, one after the other:
// as many as the console has push slots, so they fill the slots next to
// ours plus one (X32-F21: 4, XA-F21: 8).
func (p *prober) fillSlots(ctx context.Context) ([]*rawClient, error) {
	var out []*rawClient
	for range p.cfg.fam.pushClients {
		c, err := p.subscriber(ctx)
		if err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, nil
}

// whoIsServed mutes ch through the engine (a gesture) and reports which
// clients got the change: index 0 is the engine (its echo), then the raw ones.
func (p *prober) whoIsServed(ctx context.Context, eng *api.Engine, rec *engineRec, ch int, raw []*rawClient) ([]bool, error) {
	addr := chAddr(ch, "mix/on")
	if err := p.guard.check(addr, osc.Int(0)); err != nil {
		return nil, err
	}
	t0 := time.Now()
	if err := eng.Apply(gestureCmd("set", ch, "mute", "true")); err != nil {
		return nil, fmt.Errorf("mixprobe: engine apply: %w", err)
	}
	_, echo := rec.waitFor(ctx, t0, pushWait, func(m recMsg) bool { return hasChange(m, ch, "mute", "gesture") })
	served := []bool{echo}
	for _, c := range raw {
		_, got, err := c.waitFor(addr, pushWait)
		if err != nil {
			return nil, err
		}
		served = append(served, got)
	}
	if err := p.guard.check(addr, osc.Int(1)); err != nil {
		return nil, err
	}
	return served, eng.Apply(gestureCmd("set", ch, "mute", "false"))
}

func hasChange(m recMsg, ch int, field, origin string) bool {
	for _, c := range m.st.Changes {
		if c.Param.Strip.Kind == "input" && c.Param.Strip.Index == ch && c.Param.Field == field && c.Origin.Kind == origin {
			return true
		}
	}
	return false
}

// slotFinding judges who got the push: the engine (index 0) and limit-1 raw
// clients must, the last raw client (the newcomer) must not.
func slotFinding(served []bool, limit int) Finding {
	names := []string{"mixcore engine"}
	for i := 1; i < len(served); i++ {
		names = append(names, fmt.Sprintf("raw client %d", i))
	}
	var got []string
	for i, s := range served {
		got = append(got, fmt.Sprintf("%s=%t", names[i], s))
	}
	f := Finding{ID: "client-slots", Facts: []string{"X32-F21", "X32-F28"},
		Title:  fmt.Sprintf("the engine holds one slot; client %d (/xremote) gets no pushes", limit+1),
		Detail: strings.Join(got, ", ")}
	n := len(served)
	switch {
	case n == limit+1 && allTrue(served[:limit]) && !served[n-1]:
		f.Status = Confirmed
		f.Detail += fmt.Sprintf(": %d push clients incl. ours, the newcomer ignored, the write socket takes no slot", limit)
	case served[0] && !served[limit-1]:
		f.Status = Refuted
		f.Detail += fmt.Sprintf(": only %d raw clients fit next to the engine: its write socket takes a slot, or another app is still connected", limit-2)
	case !served[0] && served[n-1]:
		f.Status = Refuted
		f.Detail += ": the oldest client (ours) lost its slot to the newcomer (evict-oldest policy)"
	case allTrue(served):
		f.Status = Refuted
		f.Detail += fmt.Sprintf(": more than %d push clients are served", limit)
	default:
		f.Status = Inconclusive
	}
	return f
}

func allTrue(v []bool) bool {
	for _, b := range v {
		if !b {
			return false
		}
	}
	return true
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
