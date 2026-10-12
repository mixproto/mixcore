// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Waits used by the checks. They are generous for a console on a LAN.
const (
	replyWait   = 500 * time.Millisecond // a read reply (X32-F04)
	pushWait    = time.Second            // a push after a change (X32-F22)
	restoreWait = time.Second            // read-back of a restored value
)

// config is one mixprobe run.
type config struct {
	fam       family
	console   netip.AddrPort
	channels  []int // input channels mixprobe may change (faders down, mutes on)
	muteGroup int   // the mute group mixprobe may engage
	listen    driver.ListenPacketFunc
	prompt    Prompter
	out       io.Writer
	outDir    string // report directory; the snapshot is written there before the first write
}

// prober runs the checks of one session.
type prober struct {
	cfg   config
	rep   *Report
	w     *rawClient // write socket: never sends /xremote
	guard *guard
}

// errAborted ends a session early (Ctrl-C or a failed precondition); the
// snapshot is still restored.
var errAborted = errors.New("mixprobe: session aborted")

// run executes the whole session and always restores what it changed.
func run(ctx context.Context, cfg config) (rep *Report, err error) {
	rep = &Report{Tool: "mixprobe", StartedAt: time.Now(), Console: map[string]string{}, Settings: settings(cfg)}
	defer func() { rep.FinishedAt = time.Now() }()
	w, err := openRaw(ctx, cfg.listen, cfg.console)
	if err != nil {
		return rep, err
	}
	defer w.close()
	p := &prober{cfg: cfg, rep: rep, w: w, guard: newGuard(w, cfg)}
	defer func() { rep.Restored = p.restore() }()
	if err := p.preflight(ctx); err != nil {
		rep.Aborted = err.Error()
		return rep, err
	}
	for _, phase := range p.phases() {
		if err := phase(ctx); err != nil {
			rep.Aborted = err.Error()
			return rep, err
		}
	}
	return rep, nil
}

// preflight: amplifiers off, identify, snapshot. Nothing is written before
// the snapshot of every allowed parameter succeeded.
func (p *prober) preflight(ctx context.Context) error {
	p.say("TURN THE AMPLIFIERS OFF. mixprobe changes input faders (down only) and mutes of channels %s and mute group %d, and asks you to recall a scene and reboot the console.",
		joinInts(p.cfg.channels), p.cfg.muteGroup)
	if ok, err := p.cfg.prompt.Step(ctx, stepAmpsOff, "Turn every amplifier and powered speaker off, then press Enter."); err != nil || !ok {
		return errors.Join(errAborted, err)
	}
	if err := p.identify(); err != nil {
		return err
	}
	if err := p.guard.snapshot(replyWait); err != nil {
		return err
	}
	if err := p.guard.save(p.cfg.outDir); err != nil {
		return errors.Join(errAborted, err) // nothing written yet: no restore possible without it on disk
	}
	p.say("Snapshot of %d parameter(s) saved to %s/snapshot.json; restored at the end and on Ctrl-C.", len(p.guard.params), p.cfg.outDir)
	return nil
}

// restore puts back every written parameter and says loudly what failed.
func (p *prober) restore() []restoreResult {
	res := p.guard.restore(restoreWait)
	var failed []string
	for _, r := range res {
		if !r.OK {
			failed = append(failed, r.Address+" (snapshot "+r.Value+", read back "+r.ReadBack+")")
		}
	}
	if len(failed) == 0 {
		p.say("Restored %d parameter(s) to their snapshot values (verified by read-back).", len(res))
		return res
	}
	p.say("\n!!! NOT RESTORED: %d parameter(s). Set them on the console by hand (values in snapshot.json):\n  %s",
		len(failed), strings.Join(failed, "\n  "))
	return res
}

func (p *prober) phases() []func(context.Context) error {
	if p.cfg.fam.name == "xair" {
		return p.xairPhases()
	}
	return []func(context.Context) error{
		p.closeApps,
		p.checkInventory, p.checkWriteSocketReads, p.checkPushAndEcho, p.checkQuantization, p.checkMuteGroup,
		p.checkMeters,
		p.engineChecks,
	}
}

func (p *prober) closeApps(ctx context.Context) error {
	_, err := p.cfg.prompt.Step(ctx, stepCloseApps,
		"Close every other remote app connected to this console ("+p.cfg.fam.otherApps+").")
	return err
}

func (p *prober) say(format string, args ...any) {
	fmt.Fprintf(p.cfg.out, format+"\n", args...)
}

// add records a finding, citing the facts of this family's protocol doc.
func (p *prober) add(f Finding) {
	f.Facts = p.cfg.fam.facts(f.Facts)
	p.rep.add(p.cfg.out, f)
}

func settings(cfg config) map[string]string {
	return map[string]string{
		"family":    cfg.fam.name,
		"console":   cfg.console.String(),
		"channels":  joinInts(cfg.channels),
		"muteGroup": strconv.Itoa(cfg.muteGroup),
	}
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ",")
}

// chAddr returns the address of field (e.g. "mix/on") of input ch.
func chAddr(ch int, field string) string { return fmt.Sprintf("/ch/%02d/%s", ch, field) }
