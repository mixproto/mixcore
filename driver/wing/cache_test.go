// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

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

func (l *link) cached() *schemaCache {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cache
}

// TestCache_DroppedOnConnectFailure (review M2): a failed connect never
// leaves a schema behind for the next one.
func TestCache_DroppedOnConnectFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		l := r.link(t)
		if _, err := l.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		r.sim.Reboot(time.Minute) // refuses every connection
		if _, err := l.Connect(t.Context()); err == nil {
			t.Fatal("connect to a rebooting console succeeded")
		}
		if l.cached() != nil {
			t.Fatal("the schema cache survived a failed connect")
		}
	})
}

// TestCache_PoisonedIsCaughtByAFreshChannelList (review M2): a cache that
// no longer matches the console's /ch definitions is walked again.
func TestCache_PoisonedIsCaughtByAFreshChannelList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		l := r.link(t)
		if _, err := l.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		c := l.cached()
		bad := c.defs["/ch/1"]
		bad.Hash ^= 0x5A5A5A5A
		c.defs["/ch/1"] = bad
		defs := countClass(r.sim, simwing.ClassDefs)
		s, err := l.Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if countClass(r.sim, simwing.ClassDefs) < defs+10 {
			t.Fatal("a poisoned cache was used without a new walk")
		}
		if l.cached().defs["/ch/1"].Hash == bad.Hash {
			t.Fatal("the poisoned entry is still cached")
		}
		_ = s.Close()
	})
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
