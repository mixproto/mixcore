// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import "strings"

// Wire facts from docs/protocols/xair.md.
const (
	// DefaultPort is the mixer's UDP control port (XA-F01).
	DefaultPort = 10024
	// inputChannels: /ch/01../ch/16 on every model by the vendor tables (XA-F71).
	inputChannels = 16
	// requiredInputs: inputs every source agrees on; 13..16 are optional
	// strips, absent for the session when they do not answer (XA-F72).
	requiredInputs = 12
	// maxPushClients: /xremote pushes go to at most eight clients (XA-F20, XA-F21).
	maxPushClients = 8
	// muteGroupCount: /config/mute/1..4 (section 3.1).
	muteGroupCount = 4
	// dcaCount: /dca/1..4 (section 3.1).
	dcaCount = 4
	// snapshots: /-snap/index is 1..64 (XA-F60).
	snapshots = 64
	// micHeadamps: /headamp/01..16 are the mic preamps (XA-F110, XA-F73).
	micHeadamps = 16
	// auxHeadamp: /headamp/17, the stereo aux on the 18-channel models (XA-F110).
	auxHeadamp = 17
	// Gain laws (XA-F111, XA-F112): dB = -12 + 72 f in 144 steps (mic),
	// dB = -12 + 32 f in 64 steps (line, aux), both 0.5 dB.
	gainMinDB     = -12.0
	gainStepDB    = 0.5
	micGainSteps  = 144
	auxGainSteps  = 64
	headampFormat = "/headamp/%02d" // two digits, 1-based (XA-F110)

	// mixBuses: /bus/1..6, one digit; FX sends /fxsend/1..4 are buses 7..10
	// (send slots 07..10), section 3.1, 3.3.
	mixBuses = 6
	fxSends  = 4

	meterBank1    = "/meters/1"    // XA-F53
	addrSnapIndex = "/-snap/index" // XA-F60, XA-F61: the dashed path every client uses
)

// modelPrefixes start every model string of the family, V2 and "W" units
// included (XA-F70, XA-F96): XR12, XR16, XR18, X18, MR12, MR18, ...V2.
var modelPrefixes = []string{"XR", "X18", "MR"}

// IsModel reports whether model is an /xinfo model string of this family
// (XA-F70). Discovery uses it with the reply port to tell families apart.
func IsModel(model string) bool {
	for _, p := range modelPrefixes {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}
