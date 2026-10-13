// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package driver

import (
	"strings"
	"unicode"
)

// MaxSceneNameRunes bounds SceneRecalled.SceneName (core-api v0.5,
// state.schema.json sceneName maxLength 64).
const MaxSceneNameRunes = 64

// SceneName cleans a scene name the console reported (user content) for
// SceneRecalled.SceneName: invalid UTF-8 and control, format and line or
// paragraph separator characters are dropped, spaces at both ends trimmed,
// and the result is at most MaxSceneNameRunes runes. A driver that sets a
// name passes it through here; the supervisor does it again.
func SceneName(raw string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(raw, ""))
	clean = strings.TrimSpace(clean)
	if r := []rune(clean); len(r) > MaxSceneNameRunes {
		clean = strings.TrimSpace(string(r[:MaxSceneNameRunes]))
	}
	return clean
}
