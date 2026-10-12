// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// sceneTimeLayout formats the simulated /-undo/time value. The console's own
// format is not documented; any change of the string is what matters (X32-F62).
const sceneTimeLayout = "15:04:05"

// RecallScene loads scene n at the "desk": every value in values is applied
// and the changed ones are pushed to every /xremote client in one burst, as
// X32-F60 describes; there is no dedicated recall message. With
// Options.PushSceneMarkers the scene markers are set and pushed first
// ([UNVERIFIED] variant). Addresses are applied in sorted order.
func (s *Sim) RecallScene(n int, values map[string]osc.Arg) error {
	now := time.Now()
	s.mu.Lock()
	out, err := s.recallLocked(now, n, values)
	s.mu.Unlock()
	for _, o := range out {
		s.send(o.to, o.pkt)
	}
	return err
}

// recallLocked applies a scene and returns the pushes. Callers hold s.mu.
func (s *Sim) recallLocked(now time.Time, n int, values map[string]osc.Arg) ([]outgoing, error) {
	for addr, a := range values {
		if v, ok := s.params[addr]; !ok || a.Type != v.typ {
			return nil, fmt.Errorf("%w: %s", ErrUnknownAddress, addr)
		}
	}
	var out []outgoing
	if s.opts.PushSceneMarkers {
		s.params[addrPrepos].i = int32(n)
		s.params[addrUndoTime].s = now.Format(sceneTimeLayout)
		out = append(out, s.pushLocked(now, addrPrepos, s.params[addrPrepos], netip.AddrPort{}, true)...)
		out = append(out, s.pushLocked(now, addrUndoTime, s.params[addrUndoTime], netip.AddrPort{}, true)...)
	}
	for _, addr := range slices.Sorted(maps.Keys(values)) {
		v := s.params[addr]
		before := v.arg()
		if !v.set(values[addr]) || v.arg().Equal(before) {
			continue // a recall pushes what changed
		}
		out = append(out, s.pushLocked(now, addr, v, netip.AddrPort{}, true)...)
	}
	if s.offline(now) {
		return nil, nil // the console changed, nobody hears about it
	}
	return out, nil
}
