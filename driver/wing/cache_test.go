// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/driver"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

func (r *rig) link(t *testing.T) *link {
	t.Helper()
	l, err := r.drv.OpenLink(t.Context(), driver.Endpoint{Host: consoleIP}, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.(*link)
}

// TestProtocolMismatch_StopsDialing (review M2, go L1): the same schema
// failure three times makes the link stop dialing and say so.
func TestProtocolMismatch_StopsDialing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{UnknownPaths: []string{"/ch/1/fdr"}})
		l := r.link(t)
		for i := range maxSchemaFailures {
			if _, err := l.Connect(t.Context()); !errors.Is(err, errSchema) || errors.Is(err, ErrProtocolMismatch) {
				t.Fatalf("attempt %d: %v", i+1, err)
			}
		}
		dials := r.dials
		_, err := l.Connect(t.Context())
		if !errors.Is(err, ErrProtocolMismatch) || r.dials != dials {
			t.Fatalf("after %d schema failures: %v, dials %d -> %d", maxSchemaFailures, err, dials, r.dials)
		}
	})
}
