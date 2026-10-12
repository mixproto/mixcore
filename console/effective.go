// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"fmt"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// EffectiveChange is a strip whose effective mute changed (design CR-01).
type EffectiveChange struct {
	Strip  model.StripRef
	Mute   model.EffectiveMute
	Origin driver.Origin // of the change that caused it
}

// effective caches every strip's effective mute so a diff lists only the
// strips it changed. Guarded by Supervisor.mu.
type effective struct {
	last    map[model.StripRef]model.EffectiveMute
	changes []EffectiveChange
}

// reset takes the effective mutes of a new full state; nothing is listed.
func (e *effective) reset(st model.State) {
	e.last = map[model.StripRef]model.EffectiveMute{}
	for _, strip := range st.Strips() {
		e.last[strip.Ref] = st.EffectiveMute(strip.Ref)
	}
	e.changes = nil
}

// update recomputes after a mute-related change and lists the strips that
// changed when list is set (no full update is pending).
func (e *effective) update(st model.State, origin driver.Origin, list bool) {
	for _, strip := range st.Strips() {
		now := st.EffectiveMute(strip.Ref)
		if prev, ok := e.last[strip.Ref]; ok && prev == now {
			continue
		}
		e.last[strip.Ref] = now
		if list {
			e.changes = append(e.changes, EffectiveChange{Strip: strip.Ref, Mute: now, Origin: origin})
		}
	}
}

// affectsMute: own mutes, group states (field mute of a group strip) and
// membership change effective mutes.
func affectsMute(f model.Field) bool {
	return f == model.FieldMute || f == model.FieldMuteGroups || f == model.FieldDCAs
}

// Toggle flips a mute (an input, main, mute group or DCA) as one user gesture:
// it reads the console's current value here and writes the inverse as an
// absolute value, never a toggle command (a lost or doubled packet could
// then flip it the wrong way). Errors as Apply, plus ErrUnsupported for a
// field other than mute and model.ErrUnknownParam for an unknown strip.
func (s *Supervisor) Toggle(p model.ParamRef, origin driver.Origin) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if p.Field != model.FieldMute {
		return fmt.Errorf("console: toggle %s: %w", p, driver.ErrUnsupported)
	}
	s.mu.Lock()
	v, ok := s.state.Value(p)
	s.mu.Unlock()
	muted, isBool := v.Bool()
	if !ok || !isBool {
		return fmt.Errorf("console: toggle %s: %w", p, model.ErrUnknownParam)
	}
	return s.Apply(driver.Command{Param: p, Value: model.Bool(!muted), Origin: origin})
}
