// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package driver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Review item 8: a scene name is user content from the console: at most
// 64 runes, no control characters, valid UTF-8.
func TestSceneName_CappedAndClean(t *testing.T) {
	for in, want := range map[string]string{
		"Song 4":                   "Song 4",
		"Song\n4\x1b[31m":          "Song4[31m",
		"\u0007Bell\u0000":         "Bell",
		"bad \xff utf8":            "bad  utf8",
		strings.Repeat("ä", 70):    strings.Repeat("ä", MaxSceneNameRunes),
		"  padded  ":               "padded",
		"tab\there\u2028separator": "tabhereseparator",
	} {
		got := SceneName(in)
		if got != want || !utf8.ValidString(got) || utf8.RuneCountInString(got) > MaxSceneNameRunes {
			t.Errorf("SceneName(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzSceneName(f *testing.F) {
	f.Add("Song 4")
	f.Add("\x00\xff\u2028")
	f.Fuzz(func(t *testing.T, in string) {
		got := SceneName(in)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > MaxSceneNameRunes || SceneName(got) != got {
			t.Fatalf("SceneName(%q) = %q", in, got)
		}
	})
}
