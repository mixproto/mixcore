// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mixproto/mixcore/console"
)

// maxRoleBus is the largest bus a monitor role may name (config.schema.json).
const maxRoleBus = 64

// roleJSON is config.schema.json role (v0.4).
type roleJSON struct {
	Kind  string         `json:"kind"`
	Buses []busGrantJSON `json:"buses,omitempty"`
}

type busGrantJSON struct {
	Bus            int             `json:"bus"`
	MonitorCeiling json.RawMessage `json:"monitorCeiling,omitempty"`
}

// role parses config role: absent is the owner (a direct connection); null
// is refused, like monitorCeiling, so a typo can never widen a role.
func (c configJSON) role() (console.Role, error) {
	raw := bytes.TrimSpace(c.Role)
	if len(raw) == 0 {
		return console.Role{}, nil
	}
	if string(raw) == jsonNull {
		return console.Role{}, errors.New("role: null; omit it for the owner")
	}
	var rj roleJSON
	if err := decodeStrict(string(raw), &rj); err != nil {
		return console.Role{}, fmt.Errorf("role: %w", err)
	}
	kind, ok := console.ParseRoleKind(rj.Kind)
	if !ok {
		return console.Role{}, fmt.Errorf("role kind %q", rj.Kind)
	}
	r := console.Role{Kind: kind}
	for _, g := range rj.Buses {
		if g.Bus > maxRoleBus {
			return console.Role{}, fmt.Errorf("role bus %d above %d", g.Bus, maxRoleBus)
		}
		ceiling, err := grantCeiling(g.MonitorCeiling)
		if err != nil {
			return console.Role{}, fmt.Errorf("role bus %d: %w", g.Bus, err)
		}
		r.Buses = append(r.Buses, console.BusGrant{Bus: g.Bus, Ceiling: ceiling})
	}
	return r, r.Validate()
}

// mustRole is role for a config parseConfig accepted.
func (c configJSON) mustRole() console.Role {
	r, _ := c.role()
	return r
}

// grantCeiling parses a bus grant's monitorCeiling: absent is the SR-28
// default (6 dB, on for monitor seats), "off" is none.
func grantCeiling(raw json.RawMessage) (*console.MonitorCeiling, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return &console.MonitorCeiling{HeadroomDB: defaultHeadroomDB}, nil
	}
	return safetyJSON{MonitorCeiling: raw}.monitor()
}
