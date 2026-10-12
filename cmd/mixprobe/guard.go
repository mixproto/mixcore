// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// direction is the only way a parameter may move away from its snapshot
// value: towards less sound.
type direction uint8

const (
	downOnly direction = iota + 1 // fader level, mix/on (1 = on): never above the snapshot
	upOnly                        // mute group state (1 = engaged = muting): never below it
)

// restoreRounds: a lost packet or a console still booting gets more tries.
const restoreRounds = 3

var (
	errNotAllowed = errors.New("mixprobe: parameter is not in the allowed set")
	errUnsafe     = errors.New("mixprobe: write would raise the level above the snapshot")
	errNoSnapshot = errors.New("mixprobe: parameter was not read before")
)

// guarded is one parameter mixprobe may change.
type guarded struct {
	addr  string
	dir   direction
	snap  osc.Arg
	read  bool // snap holds the console's value
	dirty bool // written by us since the snapshot (restored at the end)
}

// guard is the single gate for every write mixprobe makes. It knows the
// allowed parameters, their snapshot values and the safe direction, and it
// restores exactly the written ones.
type guard struct {
	params []*guarded
	w      *rawClient // write socket: never sends /xremote
}

func newGuard(w *rawClient, channels []int, muteGroup int) *guard {
	g := &guard{w: w}
	for _, ch := range channels {
		g.params = append(g.params,
			&guarded{addr: fmt.Sprintf("/ch/%02d/mix/fader", ch), dir: downOnly},
			&guarded{addr: fmt.Sprintf("/ch/%02d/mix/on", ch), dir: downOnly})
	}
	g.params = append(g.params, &guarded{addr: fmt.Sprintf("/config/mute/%d", muteGroup), dir: upOnly})
	return g
}

func (g *guard) find(addr string) (*guarded, bool) {
	i := slices.IndexFunc(g.params, func(p *guarded) bool { return p.addr == addr })
	if i < 0 {
		return nil, false
	}
	return g.params[i], true
}

// snapshot reads every allowed parameter. It fails if any does not answer:
// mixprobe must not change what it cannot put back.
func (g *guard) snapshot(wait time.Duration) error {
	for _, p := range g.params {
		a, ok, err := g.w.read(p.addr, wait)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("mixprobe: %s did not answer; nothing was changed", p.addr)
		}
		p.snap, p.read = a, true
	}
	return nil
}

// value returns the snapshot value of addr.
func (g *guard) value(addr string) (osc.Arg, bool) {
	p, ok := g.find(addr)
	if !ok || !p.read {
		return osc.Arg{}, false
	}
	return p.snap, true
}

// check reports whether writing a to addr is allowed and marks it dirty.
// Callers that write through another path (the engine) call it first.
func (g *guard) check(addr string, a osc.Arg) error {
	p, ok := g.find(addr)
	if !ok {
		return fmt.Errorf("%w: %s", errNotAllowed, addr)
	}
	if !p.read {
		return fmt.Errorf("%w: %s", errNoSnapshot, addr)
	}
	if a.Type != p.snap.Type || !safeDirection(p, a) {
		return fmt.Errorf("%w: %s %s (snapshot %s)", errUnsafe, addr, argString(a), argString(p.snap))
	}
	p.dirty = true
	return nil
}

func safeDirection(p *guarded, a osc.Arg) bool {
	if !inDomain(a) {
		return false
	}
	switch {
	case a.Type == osc.TypeFloat && p.dir == downOnly:
		return a.Float <= p.snap.Float
	case a.Type == osc.TypeInt && p.dir == downOnly:
		return a.Int <= p.snap.Int
	case a.Type == osc.TypeInt && p.dir == upOnly:
		return a.Int >= p.snap.Int
	default:
		return false
	}
}

// touch marks addr for restore: something other than mixprobe (the operator
// at the desk, a scene recall, a reboot) may have changed it.
func (g *guard) touch(addr string) {
	if p, ok := g.find(addr); ok && p.read {
		p.dirty = true
	}
}

// touchAll marks every allowed parameter for restore.
func (g *guard) touchAll() {
	for _, p := range g.params {
		g.touch(p.addr)
	}
}

// inDomain: switches are 0 or 1 (X32-F33, X32-F35), levels 0..1 (X32-F32).
func inDomain(a osc.Arg) bool {
	switch a.Type {
	case osc.TypeInt:
		return a.Int == 0 || a.Int == 1
	case osc.TypeFloat:
		return a.Float >= 0 && a.Float <= 1 // false for NaN
	default:
		return false
	}
}

// save writes the snapshot to dir/snapshot.json so a killed session can be
// put back by hand.
func (g *guard) save(dir string) error {
	snap := map[string]string{}
	for _, p := range g.params {
		snap[p.addr] = argString(p.snap)
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("mixprobe: encode snapshot: %w", err)
	}
	root, err := openReportDir(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.WriteFile("snapshot.json", b, 0o600); err != nil {
		return fmt.Errorf("mixprobe: write snapshot: %w", err)
	}
	return nil
}

// write sends a guarded write from the write socket.
func (g *guard) write(addr string, a osc.Arg) error {
	if err := g.check(addr, a); err != nil {
		return err
	}
	return g.w.send(addr, a)
}

// restoreResult is one restored parameter, verified by reading it back.
type restoreResult struct {
	Address  string `json:"address"`
	Value    string `json:"value"`
	ReadBack string `json:"readBack"`
	OK       bool   `json:"ok"`
}

// restore writes the snapshot value of every parameter written since the
// snapshot and reads it back, in up to restoreRounds rounds. It returns one
// result per parameter (the last attempt) and is safe to call more than once.
func (g *guard) restore(wait time.Duration) []restoreResult {
	byAddr := map[string]restoreResult{}
	var order []string
	for range restoreRounds {
		for _, p := range g.params {
			if !p.dirty {
				continue
			}
			if _, seen := byAddr[p.addr]; !seen {
				order = append(order, p.addr)
			}
			byAddr[p.addr] = g.restoreOne(p, wait)
		}
	}
	out := make([]restoreResult, 0, len(order))
	for _, a := range order {
		out = append(out, byAddr[a])
	}
	return out
}

func (g *guard) restoreOne(p *guarded, wait time.Duration) restoreResult {
	r := restoreResult{Address: p.addr, Value: argString(p.snap)}
	g.w.drain() // drop late replies, so the read-back is this one's
	if err := g.w.send(p.addr, p.snap); err == nil {
		if back, ok, _ := g.w.read(p.addr, wait); ok {
			r.ReadBack, r.OK = argString(back), back.Equal(p.snap)
		}
	}
	if r.OK {
		p.dirty = false
	}
	return r
}
