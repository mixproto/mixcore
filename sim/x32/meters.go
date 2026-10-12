// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

import (
	"context"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

// meterLoop streams bank 1 to every meter client at its interval until its
// registration expires (X32-F50).
func (s *Sim) meterLoop(ctx context.Context) {
	t := time.NewTicker(s.opts.MeterInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, o := range s.dueMeters(now) {
				s.send(o.to, o.pkt)
			}
		}
	}
}

func (s *Sim) dueMeters(now time.Time) []outgoing {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meterSubs = slices.DeleteFunc(s.meterSubs, func(m meterSub) bool { return !now.Before(m.expires) })
	if s.offline(now) || len(s.meterSubs) == 0 {
		return nil
	}
	pkts := map[string][]byte{}
	var out []outgoing
	for i := range s.meterSubs {
		if now.Before(s.meterSubs[i].next) {
			continue
		}
		s.meterSubs[i].next = now.Add(s.meterSubs[i].interval)
		bank := s.meterSubs[i].bank
		if _, ok := pkts[bank]; !ok {
			pkts[bank] = s.meterPacket(bank)
		}
		if pkts[bank] != nil {
			out = append(out, outgoing{to: s.meterSubs[i].addr, pkt: pkts[bank]})
		}
	}
	return out
}

// meterPacket encodes one bank (X32-F51, X32-F53, X32-F55). Callers hold s.mu.
func (s *Sim) meterPacket(bank string) []byte {
	var blob []byte
	if bank == bankBuses {
		var levels [bank2MainL + 2]float32
		levels[bank2MainL], levels[bank2MainL+1] = s.mainLR[0], s.mainLR[1]
		blob = appendMeterBlob(nil, meterBank2Values, levels[:])
	} else {
		blob = appendMeterBlob(nil, meterBank1Values, s.levels[:])
	}
	pkt, err := osc.AppendString(nil, bank, osc.Blob(blob))
	if err != nil {
		return nil
	}
	return pkt
}
