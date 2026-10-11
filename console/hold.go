// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"maps"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// maxPendingEvents bounds hold events kept between two Drain calls.
const maxPendingEvents = 256

// ErrGestureEnded is returned by Apply for a fader whose hold was ended by an
// external change or by losing the console. A new hold-begin starts a new gesture.
var ErrGestureEnded = errors.New("console: gesture ended; start a new hold")

// HoldEventKind says why a hold ended without the user releasing it.
type HoldEventKind uint8

// Hold event kinds.
const (
	// HoldExternalChange: the desk, a scene recall or another client changed a
	// parameter a person was holding. The console's value is already in state.
	HoldExternalChange HoldEventKind = iota + 1
	// HoldCancelled: the hold was cancelled because the console went stale or
	// the engine stopped. No write resumes without a new hold-begin.
	HoldCancelled
)

func (k HoldEventKind) String() string {
	return [...]string{"unknown", "externalChangeOnHeld", "holdCancelled"}[min(int(k), 2)]
}

// HoldEvent reports a hold that ended without the user releasing it.
type HoldEvent struct {
	Kind     HoldEventKind
	Param    model.ParamRef
	ClientID string        // whose hold ended
	Value    model.Value   // HoldExternalChange: the console's new value
	Origin   driver.Origin // HoldExternalChange: who changed it
	Reason   string        // HoldCancelled: "stale" or "stopped"
}

type holdKey struct {
	param  model.ParamRef
	client string
}

// holds tracks active and ended gestures. Guarded by Supervisor.mu.
type holds struct {
	active map[holdKey]struct{}
	ended  map[holdKey]struct{}
	events []HoldEvent
}

func newHolds() holds {
	return holds{active: map[holdKey]struct{}{}, ended: map[holdKey]struct{}{}}
}

func (h *holds) addEvent(ev HoldEvent) {
	if len(h.events) < maxPendingEvents {
		h.events = append(h.events, ev)
	}
}

// isOwnEcho: a change that carries the holder's own gesture or macro origin is
// the echo of its write and never ends its gesture.
func isOwnEcho(o driver.Origin, client string) bool { return o.MayWrite() && o.ClientID == client }

// external ends every hold on c.Param that c did not come from.
func (h *holds) external(c Change) {
	for k := range h.active {
		if k.param != c.Param || isOwnEcho(c.Origin, k.client) {
			continue
		}
		delete(h.active, k)
		h.ended[k] = struct{}{}
		h.addEvent(HoldEvent{Kind: HoldExternalChange, Param: k.param, ClientID: k.client, Value: c.Value, Origin: c.Origin})
	}
}

// cancelAll ends every active hold (stale, stopped).
func (h *holds) cancelAll(reason string) {
	for k := range maps.Clone(h.active) {
		delete(h.active, k)
		h.ended[k] = struct{}{}
		h.addEvent(HoldEvent{Kind: HoldCancelled, Param: k.param, ClientID: k.client, Reason: reason})
	}
}

func (h *holds) isEnded(p model.ParamRef, client string) bool {
	_, ok := h.ended[holdKey{param: p, client: client}]
	return ok
}

// Hold starts (held = true) or ends (false) a person's gesture on a fader.
// Both clear an ended gesture of that client. Holding never writes.
func (s *Supervisor) Hold(p model.ParamRef, origin driver.Origin, held bool) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if p.Field != model.FieldFader {
		return fmt.Errorf("console: hold %s: %w", p, driver.ErrUnsupported)
	}
	k := holdKey{param: p, client: origin.ClientID}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.holds.ended, k)
	if held {
		s.holds.active[k] = struct{}{}
	} else {
		delete(s.holds.active, k)
	}
	return nil
}

// endHeldOnReplaceLocked treats a full state replacement (scene recall) as an
// external change of every held parameter.
func (s *Supervisor) endHeldOnReplaceLocked() {
	for k := range maps.Clone(s.holds.active) {
		v, ok := s.state.Value(k.param)
		if ok {
			s.holds.external(Change{Param: k.param, Value: v, Origin: driver.Origin{Kind: driver.OriginConsole}})
		}
	}
}
