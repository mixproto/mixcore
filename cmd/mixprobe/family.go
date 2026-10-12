// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/driver/xair"
)

// family is what mixprobe needs to know about a console family. The x32
// checks cite docs/protocols/x32.md; for xair the same checks are reported
// with the xair.md facts that confirm them (factMap) and the xair-only
// checklist items of xair.md section 13 run as their own phases.
type family struct {
	name             string
	defaultPort      int
	maxChannel       int
	maxMuteGroup     int
	defaultMuteGroup int
	pushClients      int      // /xremote push limit (X32-F21, XA-F21)
	sceneMarkers     []string // addresses a scene recall may push (X32-F61, X32-F62; XA-F60)
	otherApps        string   // examples for the "close other apps" step
	surface          string   // where the operator changes a parameter
	factMap          map[string]string
	probe            func(context.Context, driver.ListenPacketFunc, []netip.AddrPort, time.Duration) ([]x32.Found, error)
	broadcast        func() []netip.AddrPort
}

var families = map[string]family{
	"x32": {
		name: "x32", defaultPort: x32.DefaultPort, maxChannel: 32, maxMuteGroup: 6, defaultMuteGroup: 6, pushClients: 4,
		sceneMarkers: []string{"/-undo/time", "/-show/prepos/current"},
		otherApps:    "X32-Edit, M32-Edit, Mixing Station, tablets, phones", surface: "on the console",
		probe: x32.Probe, broadcast: x32.BroadcastTargets,
	},
	"xair": {
		name: "xair", defaultPort: xair.DefaultPort, maxChannel: 16, maxMuteGroup: 4, defaultMuteGroup: 4, pushClients: 8,
		sceneMarkers: []string{"/-snap/index"},
		otherApps:    "X AIR Edit, M AIR Edit, Mixing Station, tablets, phones",
		surface:      "in X AIR Edit or another app (the mixer has no faders, XA-F74)",
		// The x32.md facts the shared checks cite, and the xair.md fact or
		// section that says the same for X Air (xair.md section 11).
		factMap: map[string]string{
			"X32-F01": "XA-F01", "X32-F04": "XA section 2", "X32-F06": "XA-F03", "X32-F10": "XA-F10", "X32-F11": "XA-F10",
			"X32-F21": "XA-F21", "X32-F22": "XA-F23", "X32-F25": "XA-F25", "X32-F26": "XA-F26", "X32-F28": "XA section 13.3",
			"X32-F35": "XA section 3.2", "X32-F36": "XA section 3.2", "X32-F39": "XA section 13.19",
			"X32-F41": "XA-F41", "X32-F42": "XA-F42", "X32-F60": "XA section 7", "X32-F61": "XA-F61", "X32-F62": "XA-F62",
		},
		probe: xair.Probe, broadcast: xair.BroadcastTargets,
	},
}

// facts translates the x32.md fact IDs of a shared check to this family's.
func (f family) facts(ids []string) []string {
	if f.factMap == nil {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if t, ok := f.factMap[id]; ok {
			id = t
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
