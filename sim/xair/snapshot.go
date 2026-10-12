// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"fmt"
	"maps"
	"net/netip"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/sim/internal/oscsim"
)

// StoreSnapshot sets what slot n holds (lab setup, not a client action):
// the parameter values a load of n applies. Slots hold only the given
// addresses; the scope flags of XA-F60 are not modelled.
func (s *Sim) StoreSnapshot(n int, name string, values map[string]osc.Arg) error {
	var err error
	s.Do(func(time.Time) []oscsim.Outgoing {
		if n < 1 || n > snapshots {
			err = fmt.Errorf("%w: snapshot %d", ErrUnknownAddress, n)
			return nil
		}
		if err = s.CheckBatchLocked(values); err != nil {
			return nil
		}
		s.snaps[n] = maps.Clone(values)
		v, _ := s.ParamLocked(snapNameAddr(n))
		v.SetString(name)
		return nil
	})
	return err
}

// LoadSnapshot loads slot n as another app, MIDI or power-on would (there is
// no surface, XA-F74): the stored values are applied, the changed ones are
// pushed to every /xremote client, and /-snap/index becomes n (XA-F60).
// With Options.PushSnapIndex the index is pushed first ([TO-VERIFY] XA-F62).
func (s *Sim) LoadSnapshot(n int) error {
	var err error
	s.Do(func(now time.Time) []oscsim.Outgoing {
		var out []oscsim.Outgoing
		out, err = s.loadLocked(now, n)
		if s.OfflineLocked(now) {
			return nil // the mixer changed, nobody hears about it
		}
		return out
	})
	return err
}

func (s *Sim) loadLocked(now time.Time, n int) ([]oscsim.Outgoing, error) {
	values, ok := s.snaps[n]
	if !ok {
		return nil, fmt.Errorf("%w: snapshot %d is empty", ErrUnknownAddress, n)
	}
	idx := s.params[addrSnapIdx]
	idx.SetInt(int32(n))
	var out []oscsim.Outgoing
	if s.opts.PushSnapIndex {
		out = s.PushLocked(now, addrSnapIdx, idx, netip.AddrPort{}, true)
	}
	return append(out, s.ApplyBatchLocked(now, values)...), nil
}

// snapAction handles /-snap/load, /-snap/save and /-snap/delete ,i n
// (XA-F60). They are writes in the log; mixcore must never send them.
func (f *family) snapAction(now time.Time, from netip.AddrPort, addr string, m *osc.Message) []oscsim.Outgoing {
	a, _ := m.Arg(0)
	n := int(a.Int)
	ok := m.NumArgs() == 1 && a.Type == osc.TypeInt && n >= 1 && n <= snapshots
	s := (*Sim)(f)
	var out []oscsim.Outgoing
	switch {
	case !ok:
	case strings.HasSuffix(addr, "/load"):
		var err error
		out, err = s.loadLocked(now, n)
		ok = err == nil
	case strings.HasSuffix(addr, "/save"):
		s.snaps[n] = s.currentValues()
		s.params[addrSnapIdx].SetInt(int32(n))
	default:
		delete(s.snaps, n)
		s.params[snapNameAddr(n)].SetString("")
	}
	f.RecordLocked(now, from, oscsim.ClassWrite, addr, a, ok)
	return out
}

// currentValues copies every strip parameter (not the snapshot node).
func (s *Sim) currentValues() map[string]osc.Arg {
	out := map[string]osc.Arg{}
	for addr, v := range s.params {
		if !strings.HasPrefix(addr, "/-snap/") {
			out[addr] = v.Arg()
		}
	}
	return out
}

// HasSnapshot reports whether slot n holds anything (tests of slot 64, XA-F63).
func (s *Sim) HasSnapshot(n int) bool {
	var ok bool
	s.Do(func(time.Time) []oscsim.Outgoing {
		_, ok = s.snaps[n]
		return nil
	})
	return ok
}

func snapNameAddr(n int) string { return fmt.Sprintf("/-snap/%02d/name", n) }
