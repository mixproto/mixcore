// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"net/netip"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/driver/xair"
)

// Console families of contracts/core-api (config, discover, session).
const (
	familyX32  = "x32"
	familyXAir = "xair" // v0.2
)

// familyPorts are the default control ports (X32-F01, XA-F01).
var familyPorts = map[string]int{familyX32: x32.DefaultPort, familyXAir: xair.DefaultPort}

func knownFamily(f string) bool { _, ok := familyPorts[f]; return ok }

// defaultDrivers are the built-in drivers by family name.
func defaultDrivers() map[string]driver.Driver {
	return map[string]driver.Driver{familyX32: x32.New(x32.Config{}), familyXAir: xair.New(xair.Config{})}
}

// broadcastTargets are the family's broadcast probe targets (X32-F12, XA-F12).
func broadcastTargets(family string) []netip.AddrPort {
	if family == familyXAir {
		return xair.BroadcastTargets()
	}
	return x32.BroadcastTargets()
}

// classify tells the family of a discovery reply (discovery.schema.json):
// the source port decides (10023 x32, 10024 xair), and a model of the other
// family vetoes it; on any other port the model decides. Empty: unknown or
// contradictory, not listed.
func classify(f x32.Found) string {
	m := f.Identity.Model
	isX32, isAir := x32.IsModel(m), xair.IsModel(m)
	switch f.Endpoint.Port {
	case x32.DefaultPort:
		if !isAir {
			return familyX32
		}
	case xair.DefaultPort:
		if !isX32 {
			return familyXAir
		}
	default:
		switch {
		case isX32:
			return familyX32
		case isAir:
			return familyXAir
		}
	}
	return ""
}
