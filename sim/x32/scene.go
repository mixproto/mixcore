// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"net/netip"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/sim/internal/oscsim"
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
	var err error
	s.Do(func(now time.Time) []oscsim.Outgoing {
		var out []oscsim.Outgoing
		out, err = s.recallLocked(now, n, values)
		return out
	})
	return err
}

// recallLocked applies a scene and returns the pushes. Callers hold the core lock.
func (s *Sim) recallLocked(now time.Time, n int, values map[string]osc.Arg) ([]oscsim.Outgoing, error) {
	if err := s.CheckBatchLocked(values); err != nil {
		return nil, err
	}
	var out []oscsim.Outgoing
	if s.opts.PushSceneMarkers {
		prepos, undo := s.params[addrPrepos], s.params[addrUndoTime]
		prepos.SetInt(int32(n))
		undo.SetString(now.Format(sceneTimeLayout))
		out = append(out, s.PushLocked(now, addrPrepos, prepos, netip.AddrPort{}, true)...)
		out = append(out, s.PushLocked(now, addrUndoTime, undo, netip.AddrPort{}, true)...)
	}
	out = append(out, s.ApplyBatchLocked(now, values)...)
	if s.OfflineLocked(now) {
		return nil, nil // the console changed, nobody hears about it
	}
	return out, nil
}
