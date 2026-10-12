// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import "math"

// A song at 120 BPM, 16 bars on a loop: 8 bars of verse, 8 bars of chorus,
// a tom fill on the last beat of each half. Levels are dBFS at the input,
// pre-fader. Every function here is pure: the same time gives the same
// level, so meters are reproducible in tests and nothing is allocated.

// voice is the sound that drives one channel's meter.
type voice uint8

const (
	voiceSilent voice = iota
	voiceKick
	voiceSnare
	voiceHat
	voiceTom1
	voiceTom2
	voiceOverhead
	voiceBass
	voiceGuitar
	voiceAcoustic
	voiceKeys
	voiceLead
	voiceHarmony
	voiceChoir
	voiceSpeech
	voicePlayback
	voiceClick
)

const (
	beat        = 0.5 // seconds at 120 BPM
	eighth      = beat / 2
	sixteenth   = beat / 4
	bar         = 4 * beat
	songBars    = 16
	chorusBar   = 8 // bars 8..15 are the chorus
	phrase      = 2 * bar
	phraseSung  = 1.5 * bar // then a breath
	syllableHz  = 4.0
	noiseRateHz = 20.0 // a new jitter value per demo tick
	bleedDB     = -50.0
	silenceDB   = -90.0 // below this a level is silence (-inf)
)

// voiceLevel is the input level of v at song time t (seconds); ch varies the
// jitter between channels with the same voice.
func voiceLevel(v voice, t float64, ch int) float64 {
	j := jitter(ch, t)
	switch v {
	case voiceKick:
		return hit(-6, since(t, beat, 0), 70, bleedDB+j)
	case voiceSnare:
		return hit(-8, since(t, 2*beat, beat), 50, bleedDB+j)
	case voiceHat:
		return hit(-20+2*j, since(t, eighth, 0), 120, -56+j)
	case voiceTom1, voiceTom2:
		return tom(v, t, j)
	case voiceOverhead:
		return math.Max(hit(-16, since(t, beat, 0), 30, -40+j), hit(-18, since(t, eighth, 0), 60, -40+j))
	case voiceBass:
		return hit(-10+j, since(t, eighth, 0), 12, bleedDB+j)
	case voiceGuitar, voiceAcoustic:
		return hit(-14+j-sectionDrop(t), since(t, beat, eighth*float64(ch%2)), 8, -48+j)
	case voiceKeys:
		return -18 + 2*math.Sin(2*math.Pi*t/(2*bar)) + j/2 - sectionDrop(t)
	case voiceLead:
		return sung(-12, t, j, true)
	case voiceHarmony:
		return sung(-15, t, j, chorus(t))
	case voiceChoir:
		return sung(-20, t, j/2, chorus(t))
	case voiceSpeech:
		return -52 + j // the mic is open, nobody speaks yet
	case voicePlayback:
		return -20 + 1.5*math.Sin(2*math.Pi*t/bar) + j/2
	case voiceClick:
		return hit(-10, since(t, beat, 0), 1000, math.Inf(-1)) // a short tick
	default:
		return math.Inf(-1)
	}
}

// hit is a transient: peak at the hit, falling decay dB/s, never below floor.
func hit(peak, dt, decay, floor float64) float64 {
	l := peak - decay*dt
	if l < silenceDB {
		l = math.Inf(-1)
	}
	return math.Max(l, floor)
}

// since is the time since the last event of a grid with period p starting at offset.
func since(t, p, offset float64) float64 {
	d := math.Mod(t-offset, p)
	if d < 0 {
		d += p
	}
	return d
}

// tom plays sixteenths on the last beat of bars 8 and 16: tom 1 the first
// two, tom 2 the last two.
func tom(v voice, t, j float64) float64 {
	b := barOf(t)
	inFill := (b == chorusBar-1 || b == songBars-1) && since(t, bar, 0) >= 3*beat
	if !inFill {
		return bleedDB + j
	}
	n := int(since(t, beat, 0) / sixteenth) // 0..3
	if (v == voiceTom1) != (n < 2) {
		return bleedDB + j
	}
	return hit(-7, since(t, sixteenth, 0), 40, bleedDB+j)
}

// sung is a vocal line: phrases with syllables, a breath between phrases.
func sung(level, t, j float64, singing bool) float64 {
	if !singing || since(t, phrase, 0) >= phraseSung {
		return -45 + j
	}
	return level + 4*math.Sin(2*math.Pi*syllableHz*t) + j
}

func barOf(t float64) int { return int(math.Floor(t/bar)) % songBars }

func chorus(t float64) bool { return barOf(t) >= chorusBar }

// sectionDrop plays the band 3 dB softer in the verse.
func sectionDrop(t float64) float64 {
	if chorus(t) {
		return 0
	}
	return 3
}

// jitter is a reproducible value in -2..+2 dB per channel and tick.
func jitter(ch int, t float64) float64 {
	k := uint64(int64(t*noiseRateHz)) ^ uint64(ch)<<40
	return 4*float64(splitmix(k)>>11)/(1<<53) - 2
}

// splitmix is the SplitMix64 finalizer (a public-domain integer hash).
func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
