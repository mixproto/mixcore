// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import "slices"

// Wire facts from docs/protocols/x32.md.
const (
	// DefaultPort is the console's UDP control port (X32-F01).
	DefaultPort = 10023
	// inputChannels: /ch/01../ch/32 (X32-F30).
	inputChannels = 32
	// maxPushClients: /xremote pushes go to at most four clients (X32-F21).
	maxPushClients = 4
	// muteGroupCount: /config/mute/1..6 (X32-F35).
	muteGroupCount = 6
	// dcaCount: /dca/1..8 (X32-F37).
	dcaCount = 8
	// Headamps (X32-F70): /headamp/NNN, three digits, 0-based; model index
	// NNN + 1. 000..031 local, 032..079 AES50 A, 080..127 AES50 B (48 per
	// port per S1; the B numbering is unverified, item 16).
	localHeadamps  = 32
	aes50Headamps  = 48
	headampFormat  = "/headamp/%03d"
	headampWireOff = -1 // wire NNN = model index - 1
	// Gain law (X32-F71, X32-F72): dB = -12 + 72 f, f = k/144, 0.5 dB.
	gainMinDB  = -12.0
	gainStepDB = 0.5
	gainSteps  = 144

	meterBank1   = "/meters/1"             // X32-F51, X32-F52
	meterBank2   = "/meters/2"             // X32-F55
	addrUndoTime = "/-undo/time"           // X32-F62
	addrPrepos   = "/-show/prepos/current" // X32-F61
)

// models are the model strings of X32-F11.
var models = []string{"X32", "X32RACK", "X32C", "X32P", "X32CORE", "M32", "M32C", "M32R"}

// IsModel reports whether model is an /xinfo model string of this family
// (X32-F11). Discovery uses it with the reply port to tell families apart.
func IsModel(model string) bool { return slices.Contains(models, model) }
