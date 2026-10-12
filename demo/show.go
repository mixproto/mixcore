// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"math"
	"slices"
)

// The show: a small church or club band. Values are a demo choice, not
// protocol facts; addresses and encodings are cited where they are used.

// Colour indexes of an xair strip, docs/protocols/xair.md section 3.2
// (config/color: OFF, RD, GN, YE, BL, MG, CY, WH).
const (
	colourRed     int32 = 1
	colourGreen   int32 = 2
	colourYellow  int32 = 3
	colourBlue    int32 = 4
	colourMagenta int32 = 5
	colourCyan    int32 = 6
	colourWhite   int32 = 7
)

// Mute groups and DCAs, as bits of a membership mask (group n = bit n-1).
const (
	mgDrums  uint8 = 1 << 0
	mgBand   uint8 = 1 << 1
	mgVox    uint8 = 1 << 2
	mgSpeech uint8 = 1 << 3 // engaged: the pastor is not speaking yet

	dcaDrums    uint8 = 1 << 0
	dcaBand     uint8 = 1 << 1
	dcaVox      uint8 = 1 << 2
	dcaPlayback uint8 = 1 << 3

	engagedMuteGroups = mgSpeech
	mainFaderDB       = 0.0
)

var off = math.Inf(-1)

// channel is one input strip of the show.
type channel struct {
	name    string
	faderDB float64
	muted   bool    // the channel's own mute
	pan     float64 // -1 left .. +1 right
	colour  int32   // xair only
	groups  uint8   // mute groups
	dcas    uint8
	voice   voice
}

// channels 1..16 are the xair show; x32 adds 17..32.
var channels = [...]channel{
	{"Kick", -3, false, 0, colourRed, mgDrums | mgBand, dcaDrums, voiceKick},
	{"Snare", -5, false, 0, colourRed, mgDrums | mgBand, dcaDrums, voiceSnare},
	{"Hi-Hat", -12, false, 0.3, colourRed, mgDrums | mgBand, dcaDrums, voiceHat},
	{"Tom 1", -8, false, 0.2, colourRed, mgDrums | mgBand, dcaDrums, voiceTom1},
	{"Tom 2", -8, false, -0.3, colourRed, mgDrums | mgBand, dcaDrums, voiceTom2},
	{"Bass", -4, false, 0, colourYellow, mgBand, dcaBand, voiceBass},
	{"Gtr L", -9, false, -0.7, colourGreen, mgBand, dcaBand, voiceGuitar},
	{"Gtr R", -9, false, 0.7, colourGreen, mgBand, dcaBand, voiceGuitar},
	{"Keys", -10, false, 0, colourCyan, mgBand, dcaBand, voiceKeys},
	{"Vox 1", -2, false, 0, colourMagenta, mgVox, dcaVox, voiceLead},
	{"Vox 2", -6, false, -0.2, colourMagenta, mgVox, dcaVox, voiceHarmony},
	{"Vox 3", -6, false, 0.2, colourMagenta, mgVox, dcaVox, voiceHarmony},
	{"Pastor mic", -1, false, 0, colourWhite, mgSpeech, 0, voiceSpeech},
	{"Ac Gtr", -10, false, 0.3, colourGreen, mgBand, dcaBand, voiceAcoustic},
	{"Playback L", -12, false, -1, colourBlue, 0, dcaPlayback, voicePlayback},
	{"Playback R", -12, false, 1, colourBlue, 0, dcaPlayback, voicePlayback},
	{"OH L", -14, false, -0.6, colourRed, mgDrums | mgBand, dcaDrums, voiceOverhead},
	{"OH R", -14, false, 0.6, colourRed, mgDrums | mgBand, dcaDrums, voiceOverhead},
	{"Floor Tom", -8, false, -0.5, colourRed, mgDrums | mgBand, dcaDrums, voiceTom2},
	{"Kick Sub", -10, false, 0, colourRed, mgDrums | mgBand, dcaDrums, voiceKick},
	{"Snare Bot", -15, false, 0, colourRed, mgDrums | mgBand, dcaDrums, voiceSnare},
	{"Bass Mic", -12, false, 0, colourYellow, mgBand, dcaBand, voiceBass},
	{"Keys 2", -12, false, 0, colourCyan, mgBand, dcaBand, voiceKeys},
	{"Synth", -14, false, 0, colourCyan, mgBand, dcaBand, voiceKeys},
	{"Choir L", -10, false, -0.5, colourMagenta, mgVox, dcaVox, voiceChoir},
	{"Choir R", -10, false, 0.5, colourMagenta, mgVox, dcaVox, voiceChoir},
	{"Host mic", -5, true, 0, colourWhite, mgSpeech, 0, voiceSpeech},
	{"Vox 4", -6, false, 0, colourMagenta, mgVox, dcaVox, voiceHarmony},
	{"Click", 0, true, 0, 0, 0, 0, voiceClick},
	{"Talkback", 0, true, 0, 0, 0, 0, voiceSilent},
	{"Spare", off, false, 0, 0, 0, 0, voiceSilent},
	{"Spare", off, false, 0, 0, 0, 0, voiceSilent},
}

// preamp is a channel's input stage: its headamp's Gain and 48 V, or a source
// with no headamp (on x32 the playback and click come from the card).
type preamp struct {
	gainDB    float64
	phantom   bool
	noHeadamp bool // x32 only: /-ha/NN/index -1 (X32-F74)
}

// preamps follow channels, one per input.
var preamps = [len(channels)]preamp{
	{gainDB: 20}, {gainDB: 25}, {gainDB: 30, phantom: true}, {gainDB: 22}, {gainDB: 22}, // Kick..Tom 2
	{gainDB: 10, phantom: true}, {gainDB: 32}, {gainDB: 32}, {gainDB: 5}, // Bass DI, Gtr L/R, Keys
	{gainDB: 32.5}, {gainDB: 34}, {gainDB: 34}, {gainDB: 38, phantom: true}, // Vox 1-3, Pastor mic
	{gainDB: 18, phantom: true}, {noHeadamp: true}, {noHeadamp: true}, // Ac Gtr, Playback L/R
	{gainDB: 28, phantom: true}, {gainDB: 28, phantom: true}, {gainDB: 22}, {gainDB: 18}, // OH L/R, Floor Tom, Kick Sub
	{gainDB: 28}, {gainDB: 25}, {gainDB: 5}, {gainDB: 5}, // Snare Bot, Bass Mic, Keys 2, Synth
	{gainDB: 40, phantom: true}, {gainDB: 40, phantom: true}, {gainDB: 36, phantom: true}, {gainDB: 34}, // Choir L/R, Host, Vox 4
	{noHeadamp: true}, {gainDB: 30, phantom: true}, {}, {}, // Click, Talkback, Spare, Spare
}

// monitorBus is an xair monitor mix: its name and the channels the
// musician on it wants loud ("more me").
type monitorBus struct {
	name string
	me   []int // 1-based channels
}

// Monitor sends, all pre-fader (xair.md section 3.3).
const (
	sendMeDB    = 0.0
	sendVoiceDB = -6.0
	sendBandDB  = -15.0
	busFaderDB  = 0.0
	dcaFaderDB  = 0.0
)

var monitorBuses = [...]monitorBus{
	{"Drum IEM", []int{1, 2, 3, 4, 5}},
	{"Bass IEM", []int{6}},
	{"Gtr wedge", []int{7, 8, 14}},
	{"Keys IEM", []int{9}},
	{"Vox wedge", []int{10, 11, 12}},
	{"Pastor", []int{13}},
}

var dcaNames = [...]string{"Drums", "Band", "Vox", "Playback"}

// sendDB is the level of channel ch (1-based) in a bus's mix.
func sendDB(b monitorBus, ch int) float64 {
	c := channels[ch-1]
	switch {
	case c.voice == voiceSilent || c.voice == voiceClick:
		return off
	case slices.Contains(b.me, ch):
		return sendMeDB
	case c.groups&(mgVox|mgSpeech) != 0:
		return sendVoiceDB
	default:
		return sendBandDB
	}
}

// deskMove is one scripted change at the "desk" (Config.DeskChanges).
type deskMove struct {
	ch      int // 1-based
	deltaDB float64
}

// deskScript returns every moved fader to where it started.
var deskScript = [...]deskMove{{10, 3}, {9, -4}, {7, 2}, {10, -3}, {9, 4}, {7, -2}}
