// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Roles are enforced here, below every UI (CLAUDE.md invariant 4,
// dybyskz-mix docs/product/roles.md, design SR-09): a write the role of its
// client does not allow is refused before any other rule and never reaches
// the console.

// ErrRole: the role of the command's client does not allow it.
var ErrRole = errors.New("console: the client's role does not allow this")

// ReasonRole is the HoldCancelled reason when a role change took a held
// parameter out of the client's reach.
const ReasonRole = "role"

// maxHeadroomDB bounds a monitor ceiling's headroom (phone-safety.md 7: 0 to +10 dB).
const maxHeadroomDB = 10.0

// ceilingFloorDB: a level the engineer has off (-inf) counts as the bottom
// step, -60 dB (CR-21), for the monitor ceiling, so a musician's +1 from
// -inf still reaches -60 dB (AC-MV-16). Open question, core-api README v0.4.
const ceilingFloorDB = -60.0

// RoleKind is what a client may do.
type RoleKind uint8

// Role kinds. The zero value is the owner: the device user on a direct
// connection (roles.md). A hub must set a role per paired device.
const (
	RoleOwner    RoleKind = iota // every write the core offers
	RoleEngineer                 // every write the core offers (no role changes, none exist in the core)
	RoleMonitor                  // sends into its buses and those buses' fader and mute
	RoleViewer                   // nothing
)

var roleNames = [...]string{"owner", "engineer", "monitor", "viewer"}

func (k RoleKind) String() string {
	if int(k) < len(roleNames) {
		return roleNames[k]
	}
	return fmt.Sprintf("RoleKind(%d)", k)
}

// ParseRoleKind parses the String form of a role kind.
func ParseRoleKind(s string) (RoleKind, bool) {
	i := slices.Index(roleNames[:], s)
	return RoleKind(max(i, 0)), i >= 0
}

// BusGrant is one bus a monitor role may write, with its monitor ceiling
// (SR-28: per bus, default +6 dB; nil = off).
type BusGrant struct {
	Bus     int
	Ceiling *MonitorCeiling
	// FX: the grant may reach an FX send master (bus kind fx); without
	// it a monitor seat writes only into mix buses (review Sec L3).
	FX bool
}

// Role is what one client may write.
type Role struct {
	Kind  RoleKind
	Buses []BusGrant // RoleMonitor only: at least one, each bus once
}

// Validate checks a role: a monitor names its buses, others name none.
func (r Role) Validate() error {
	if r.Kind > RoleViewer {
		return fmt.Errorf("console: role %v: unknown kind", r.Kind)
	}
	if r.Kind != RoleMonitor {
		if len(r.Buses) > 0 {
			return fmt.Errorf("console: a %s role has no buses", r.Kind)
		}
		return nil
	}
	if len(r.Buses) == 0 {
		return errors.New("console: a monitor role needs its buses")
	}
	seen := map[int]bool{}
	for _, g := range r.Buses {
		if g.Bus < 1 || seen[g.Bus] {
			return fmt.Errorf("console: monitor bus %d (from 1, each once)", g.Bus)
		}
		seen[g.Bus] = true
		if c := g.Ceiling; c != nil && !(c.HeadroomDB >= 0 && c.HeadroomDB <= maxHeadroomDB) {
			return fmt.Errorf("console: monitor bus %d headroom %v outside 0..%v dB", g.Bus, c.HeadroomDB, maxHeadroomDB)
		}
	}
	return nil
}

// grant returns the grant of bus b.
func (r Role) grant(b int) (BusGrant, bool) {
	i := slices.IndexFunc(r.Buses, func(g BusGrant) bool { return g.Bus == b })
	if i < 0 {
		return BusGrant{}, false
	}
	return r.Buses[i], true
}

// mixBus is the bus a parameter a monitor may write belongs to: a send
// level, on or pan into bus b, or the fader or mute of bus b; 0 otherwise.
func mixBus(p model.ParamRef) int {
	switch {
	case p.Field == model.FieldSendLevel || p.Field == model.FieldSendOn || p.Field == model.FieldSendPan:
		return p.Bus
	case p.Strip.Kind == model.StripBus && (p.Field == model.FieldFader || p.Field == model.FieldMute):
		return p.Strip.Index
	}
	return 0
}

// allowsLocked reports whether role r may write p on the current console:
// a monitor needs a grant for p's bus, an FX grant for an FX bus (review
// Sec L3), and a grant for the partner bus too when p acts on both buses
// of a pair: the pair is linked (the console may mirror the write, X32-F81
// item 25) or p is the pair's shared send pan (X32-F84) (review Sec M2).
func (s *Supervisor) allowsLocked(r Role, p model.ParamRef) bool {
	switch r.Kind {
	case RoleOwner, RoleEngineer:
		return true
	case RoleMonitor:
	default:
		return false
	}
	b := mixBus(p)
	g, ok := r.grant(b)
	if !ok {
		return false
	}
	info, known := s.busInfoLocked(b)
	if known && info.Kind == model.BusFX && !g.FX {
		return false
	}
	if partner, shared := s.pairPartnerLocked(p, b, info); shared {
		_, ok = r.grant(partner)
	}
	return ok
}

// busInfoLocked describes bus b of the current state.
func (s *Supervisor) busInfoLocked(b int) (model.BusInfo, bool) {
	for _, info := range s.state.BusInfos() {
		if info.Index == b {
			return info, true
		}
	}
	return model.BusInfo{}, false
}

// pairPartnerLocked returns the other bus of b's pair when a write of p
// acts on both: the pair is linked, or p is a pair-scoped send pan.
func (s *Supervisor) pairPartnerLocked(p model.ParamRef, b int, info model.BusInfo) (int, bool) {
	if !info.Linkable {
		return 0, false
	}
	odd := b - (1 - b%2)
	partner := odd + 1
	if b == partner {
		partner = odd
	}
	if _, ok := s.busInfoLocked(partner); !ok {
		return 0, false
	}
	linked, _ := s.state.Value(model.ParamRef{Strip: model.StripRef{Kind: model.StripBus, Index: odd}, Field: model.FieldLinked})
	on, _ := linked.Bool()
	return partner, on || (p.Field == model.FieldSendPan && s.state.Shape().Sends.PairScoped)
}

// recheckHoldsLocked ends every hold its client's role no longer allows
// (a role change, or a pair linked at the console).
func (s *Supervisor) recheckHoldsLocked(only func(client string) bool) {
	for k := range maps.Clone(s.holds.active) {
		if only != nil && !only(k.client) {
			continue
		}
		if !s.allowsLocked(s.safe.role(k.client), k.param) {
			s.holds.cancel(k, ReasonRole)
			s.closeWindowLocked(k)
		}
	}
}

// mixesConsole: the role may change console-wide settings (show mode,
// Gain unlocks): owner and engineer.
func (r Role) mixesConsole() bool { return r.Kind == RoleOwner || r.Kind == RoleEngineer }

// role returns the role of a client. Guarded by Supervisor.mu.
func (s *safety) role(client string) Role {
	if r, ok := s.roles[client]; ok {
		return r
	}
	return s.defRole
}

// SetClientRole sets the role of one client (the hub, from its pairing).
// It applies at once: every hold of that client its new role does not
// allow is cancelled (HoldCancelled, ReasonRole); the windows of the holds
// it keeps are reopened under the new role (a monitor ceiling applies from
// the next set). It writes nothing.
func (s *Supervisor) SetClientRole(clientID string, r Role) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.safe.roles[clientID] = r
	s.safe.forgetSeat(clientID, time.Now()) // its values stop masking once older than the echo window
	defer s.syncHeldLocked()
	s.recheckHoldsLocked(func(c string) bool { return c == clientID })
	for k := range s.holds.active {
		if old, ok := s.safe.windows[k]; ok && k.client == clientID {
			s.reopenWindowLocked(k, old)
		}
	}
	s.signal()
	return nil
}

// reopenWindowLocked opens the window of a kept hold under the client's new
// role (a new ceiling) and keeps what the hold already did: its start, its
// last write and whether it wrote.
func (s *Supervisor) reopenWindowLocked(k holdKey, old *window) {
	w := s.openWindowLocked(k)
	w.Start, w.lastWrite, w.wrote = old.Start, old.lastWrite, old.wrote
	if w.HasCap {
		w.CapUp, w.CapDown = math.Max(w.CapUp, old.Start), math.Min(w.CapDown, old.Start)
	}
}

// checkRoleLocked refuses a write, hold or setting outside the client's role.
func (s *Supervisor) checkRoleLocked(p model.ParamRef, origin driver.Origin) error {
	if !s.allowsLocked(s.safe.role(origin.ClientID), p) {
		return fmt.Errorf("%w: %s may not change %s", ErrRole, s.safe.role(origin.ClientID).Kind, p)
	}
	return nil
}

// checkConsoleRoleLocked refuses a console-wide setting outside owner and engineer.
func (s *Supervisor) checkConsoleRoleLocked(origin driver.Origin) error {
	if r := s.safe.role(origin.ClientID); !r.mixesConsole() {
		return fmt.Errorf("%w: %s may not change console settings", ErrRole, r.Kind)
	}
	return nil
}

// SetShowModeBy turns show mode on or off for a client's gesture, which
// only owner and engineer roles may do (ErrRole). It writes nothing.
func (s *Supervisor) SetShowModeBy(origin driver.Origin, on bool) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	s.mu.Lock()
	err := s.checkConsoleRoleLocked(origin)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.SetShowMode(on)
	return nil
}

// ceilingOf is the monitor ceiling for a client's write to p: a monitor
// role's grant for the bus of p wins (nil = off); otherwise the client's
// own safety setting (safety.monitorCeiling).
func (s *safety) ceilingOf(p model.ParamRef, client string) *MonitorCeiling {
	if r := s.role(client); r.Kind == RoleMonitor {
		if g, ok := r.grant(mixBus(p)); ok {
			return g.Ceiling
		}
	}
	return s.client(client).Monitor
}

// isCeilingField: the level fields a monitor ceiling caps (SR-28: each
// send and My level, and every fader).
func isCeilingField(f model.Field) bool { return f == model.FieldFader || f == model.FieldSendLevel }

// finiteOr returns db, or alt when db is -inf (the engineer has the send off).
func finiteOr(db, alt float64) float64 {
	if math.IsInf(db, -1) {
		return alt
	}
	return db
}
