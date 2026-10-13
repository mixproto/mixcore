// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"strings"
	"testing"
)

// FuzzParsePINRecord (review sec L4): a stored record comes back through
// the config: any input is refused or read; an accepted record has the
// documented shape and prints back to itself.
func FuzzParsePINRecord(f *testing.F) {
	f.Add("pbkdf2-sha256$20000$c2FsdHNhbHRzYWx0c2FsdA$Zm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyMTI")
	f.Add("pbkdf2-sha256$1$x$y")
	f.Add("pbkdf2-sha256$999999999999999999999$$")
	f.Add("$$$")
	f.Fuzz(func(t *testing.T, in string) {
		r, err := parsePINRecord(in)
		if err != nil {
			return
		}
		if r.iter < pinMinIterations || r.iter > pinMaxIterations || len(r.salt) != pinSaltBytes || len(r.key) != pinKeyBytes {
			t.Fatalf("accepted %q: %+v", in, r)
		}
		if again, err := parsePINRecord(r.String()); err != nil || again.String() != r.String() || !strings.HasPrefix(r.String(), pinRecordPrefix+"$") {
			t.Fatalf("round trip of %q: %v", in, err)
		}
	})
}
