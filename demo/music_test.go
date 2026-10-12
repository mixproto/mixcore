// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package demo

import (
	"math"
	"testing"
)

const songSeconds = songBars * bar

// Every voice gives a finite level at or below 0 dBFS, or -inf, over the whole song.
func TestVoiceLevel_RangeOverTheSong(t *testing.T) {
	for v := voiceSilent; v <= voiceClick; v++ {
		for k := range int(songSeconds * noiseRateHz) {
			l := voiceLevel(v, float64(k)/noiseRateHz, 3)
			if math.IsNaN(l) || l > 0 || (math.IsInf(l, 0) && !math.IsInf(l, -1)) {
				t.Fatalf("voice %d at %v s: %v dBFS", v, float64(k)/noiseRateHz, l)
			}
		}
	}
}

// Meters move like music: each sounding voice spans a range, a silent one stays silent.
func TestVoiceLevel_MovesLikeMusic(t *testing.T) {
	const minSwingDB = 6
	for v := voiceKick; v <= voiceClick; v++ {
		lo, hi := math.Inf(1), math.Inf(-1)
		for k := range int(songSeconds * noiseRateHz) {
			l := voiceLevel(v, float64(k)/noiseRateHz, 1)
			lo, hi = math.Min(lo, l), math.Max(hi, l)
		}
		if v == voiceSpeech || v == voicePlayback { // an open mic, a mastered track: small swings only
			if hi-lo < 2 {
				t.Fatalf("voice %d: %v..%v dBFS", v, lo, hi)
			}
			continue
		}
		if hi-lo < minSwingDB {
			t.Fatalf("voice %d swings only %v..%v dBFS", v, lo, hi)
		}
	}
	if l := voiceLevel(voiceSilent, 3.1, 1); !math.IsInf(l, -1) {
		t.Fatalf("silent = %v", l)
	}
}

func TestVoiceLevel_Rhythm(t *testing.T) {
	tests := []struct {
		name    string
		v       voice
		t       float64
		wantMin float64 // level at least
		wantMax float64 // level at most
	}{
		{"kick on beat 1", voiceKick, 0, -7, -5},
		{"kick decays before the next beat", voiceKick, 0.45, -50, -30},
		{"snare on beat 2", voiceSnare, beat, -9, -7},
		{"snare has decayed by beat 3", voiceSnare, 2*beat + 0.01, -40, -30},
		{"tom 1 in the fill of bar 8", voiceTom1, 7*bar + 3*beat, -8, -6},
		{"tom 2 waits for the second half of the fill", voiceTom2, 7*bar + 3*beat, -53, -47},
		{"tom 2 in the fill", voiceTom2, 7*bar + 3*beat + 2*sixteenth, -8, -6},
		{"no tom outside a fill", voiceTom1, 3 * bar, -53, -47},
		{"lead sings", voiceLead, 0.3, -19, -5},
		{"lead breathes", voiceLead, phraseSung + 0.1, -48, -42},
		{"harmony waits for the chorus", voiceHarmony, 0.3, -48, -42},
		{"harmony in the chorus", voiceHarmony, chorusBar*bar + 0.3, -22, -8},
		{"click on the beat", voiceClick, beat, -11, -9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if l := voiceLevel(tc.v, tc.t, 2); l < tc.wantMin || l > tc.wantMax {
				t.Fatalf("level %v dBFS, want %v..%v", l, tc.wantMin, tc.wantMax)
			}
		})
	}
	if l := voiceLevel(voiceClick, beat/2, 2); !math.IsInf(l, -1) {
		t.Fatalf("click between beats = %v, want -inf", l)
	}
}

func TestJitter_ReproducibleAndBounded(t *testing.T) {
	if jitter(4, 1.23) != jitter(4, 1.23) {
		t.Fatal("same channel and time must give the same jitter")
	}
	differs := false
	for k := range 1000 {
		j := jitter(k%32, float64(k)/noiseRateHz)
		if j < -2 || j >= 2 {
			t.Fatalf("jitter %v outside -2..+2", j)
		}
		differs = differs || j != jitter(k%32+1, float64(k)/noiseRateHz)
	}
	if !differs {
		t.Fatal("channels share one jitter")
	}
}

func TestMainMix(t *testing.T) {
	levels := []float64{-10, -10, math.Inf(-1)}
	centre := []stripMix{{faderDB: 0, audible: true}, {faderDB: 0, audible: false}, {faderDB: 0, audible: true}}
	l, r := mainMix(levels, centre, 0)
	if !approx(l, -10) || !approx(r, -10) {
		t.Fatalf("one audible input at -10: %v / %v", l, r)
	}
	twoLoud := []stripMix{{audible: true}, {audible: true}, {audible: true}}
	if l, _ := mainMix(levels, twoLoud, 0); !approx(l, -10+10*math.Log10(2)) {
		t.Fatalf("two inputs add as power: %v", l)
	}
	left := []stripMix{{faderDB: -6, audible: true, pan: -1}}
	if l, r := mainMix(levels[:1], left, -4); !approx(l, -20) || !math.IsInf(r, -1) {
		t.Fatalf("hard left at fader -6, main -4: %v / %v", l, r)
	}
	if l, r := mainMix(levels, centre, math.Inf(-1)); !math.IsInf(l, -1) || !math.IsInf(r, -1) {
		t.Fatal("main at -inf must be silent")
	}
	if l, _ := mainMix([]float64{6}, []stripMix{{faderDB: 10, audible: true}}, 10); l != mainCeilingDB {
		t.Fatalf("main above 0 dBFS = %v, want the ceiling", l)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
