// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mixproto/mixcore/internal/osc"
)

// checkMuteGroup engages the allowed mute group, asks whether its button is
// lit (polarity, X32-F35) and checks that members' own mutes are untouched
// (the "OR" model of X32-F39). Then it releases the group again.
func (p *prober) checkMuteGroup(ctx context.Context) error {
	addr := fmt.Sprintf("/config/mute/%d", p.cfg.muteGroup)
	snap, _ := p.guard.value(addr)
	facts := []string{"X32-F35"}
	if snap.Int != 0 {
		p.add(Finding{ID: "mute-group", Facts: facts, Title: "mute group polarity", Status: Skipped,
			Detail: addr + " is already 1 (engaged?); release the group on the console and run again"})
		return nil
	}
	s, err := p.subscriber(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	if err := p.guard.write(addr, osc.Int(1)); err != nil {
		return err
	}
	_, pushed, err := s.waitFor(addr, pushWait)
	if err != nil {
		return err
	}
	lit, answered, err := p.cfg.prompt.Ask(ctx, stepMuteGroupOn,
		fmt.Sprintf("Is the MUTE GROUP %d button lit on the console now (group engaged)?", p.cfg.muteGroup))
	if err != nil {
		return err
	}
	members, err := p.membersUnchanged()
	if err != nil {
		return err
	}
	if err := p.guard.write(addr, snap); err != nil {
		return err
	}
	p.add(polarityFinding(addr, pushed, lit, answered))
	p.add(members)
	return nil
}

func polarityFinding(addr string, pushed, lit, answered bool) Finding {
	f := Finding{ID: "mute-group-polarity", Facts: []string{"X32-F35"}, Title: "/config/mute/N = 1 engages the group and is pushed",
		Evidence: map[string]string{"pushed": fmt.Sprint(pushed), "operator says lit": fmt.Sprint(lit)}}
	switch {
	case !answered:
		f.Status, f.Detail = Inconclusive, "operator skipped the question; pushed = "+fmt.Sprint(pushed)
	case lit && pushed:
		f.Status, f.Detail = Confirmed, "wrote "+addr+" 1: the group button lit and the change was pushed"
	case lit:
		f.Status, f.Detail = Refuted, "the group engaged but the change was not pushed to the /xremote socket"
	default:
		f.Status, f.Detail = Refuted, "wrote "+addr+" 1 but the group button did not light: polarity differs"
	}
	return f
}

// membersUnchanged reads the own mute of allowed channels that are members of
// the engaged group: under the OR model they keep their snapshot value.
func (p *prober) membersUnchanged() (Finding, error) {
	f := Finding{ID: "mute-group-or-model", Facts: []string{"X32-F36", "X32-F39"},
		Title: "engaging a group does not rewrite its members' own mute", Evidence: map[string]string{}}
	bit := int32(1) << (p.cfg.muteGroup - 1)
	var members, changed []string
	for _, ch := range p.cfg.channels {
		grp, ok, err := p.w.read(chAddr(ch, "grp/mute"), replyWait)
		if err != nil {
			return f, err
		}
		if !ok || grp.Int&bit == 0 {
			continue
		}
		members = append(members, fmt.Sprint(ch))
		on, ok, err := p.w.read(chAddr(ch, "mix/on"), replyWait)
		if err != nil {
			return f, err
		}
		if snap, _ := p.guard.value(chAddr(ch, "mix/on")); !ok || on.Int != snap.Int {
			changed = append(changed, fmt.Sprint(ch))
		}
	}
	f.Evidence["members among allowed channels"] = strings.Join(members, ",")
	switch {
	case len(members) == 0:
		f.Status = Inconclusive
		f.Detail = fmt.Sprintf("no allowed channel is a member of mute group %d; assign one on the console to check this", p.cfg.muteGroup)
	case len(changed) == 0:
		f.Status, f.Detail = Confirmed, "members' mix/on unchanged while the group is engaged"
	default:
		f.Status, f.Detail = Refuted, "the console rewrote mix/on of channel(s) "+strings.Join(changed, ",")
	}
	return f, nil
}
