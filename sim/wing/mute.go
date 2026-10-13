// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"fmt"
	"strconv"
	"strings"
)

// SetMuteGroupMember makes channel ch (1..40) a member of mute group g
// (1..8) or not. A test control: how the console stores the membership is
// unknown (item 11), so it is not in the tree; only its effect on $mute is.
func (s *Sim) SetMuteGroupMember(ch, g int, member bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if member {
		s.members[ch] |= 1 << (g - 1)
	} else {
		s.members[ch] &^= 1 << (g - 1)
	}
	s.pushAll(s.recomputeMutes(), nil)
}

// recomputeMutes sets every strip's $mute (wing.md 3.2): 1 for its own
// mute; GroupMuteValue when an engaged member mute group, or (with
// /cfg/dcamgrp = 1) a muted DCA of its tags, mutes it; else 0. It returns
// the $mute nodes that changed.
func (s *Sim) recomputeMutes() []*node {
	var changed []*node
	for _, root := range []string{"/ch", "/aux", "/bus", "/main", "/mtx"} {
		group := s.tree.byPath[root]
		if group == nil {
			continue
		}
		for _, strip := range group.children {
			eff := strip.child("$mute")
			if eff == nil {
				continue
			}
			changed = append(changed, s.setDerived(eff, intVal(s.muteOf(strip)))...)
		}
	}
	return changed
}

func (s *Sim) muteOf(strip *node) int32 {
	if own := strip.child("mute"); own != nil && own.val.i == 1 {
		return 1
	}
	if s.groupMuted(strip) || s.dcaMuted(strip) {
		return s.opts.GroupMuteValue
	}
	return 0
}

func (s *Sim) groupMuted(strip *node) bool {
	if !strings.HasPrefix(strip.path, "/ch/") {
		return false
	}
	ch, _ := strconv.Atoi(strip.name)
	for g := 1; g <= muteGroups; g++ {
		if s.members[ch]&(1<<(g-1)) != 0 && s.intAt(fmt.Sprintf("/mgrp/%d/mute", g)) == 1 {
			return true
		}
	}
	return false
}

// dcaMuted: /cfg/dcamgrp = 1 and a muted DCA among the strip's #Dn tags.
func (s *Sim) dcaMuted(strip *node) bool {
	tags := strip.child("tags")
	if tags == nil || s.intAt("/cfg/dcamgrp") != 1 {
		return false
	}
	for _, tag := range strings.FieldsFunc(tags.val.s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if d, err := strconv.Atoi(strings.TrimPrefix(tag, "#D")); err == nil && strings.HasPrefix(tag, "#D") && s.intAt(fmt.Sprintf("/dca/%d/mute", d)) == 1 {
			return true
		}
	}
	return false
}

func (s *Sim) intAt(path string) int32 {
	if n := s.tree.byPath[path]; n != nil {
		return n.val.i
	}
	return 0
}
