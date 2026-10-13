// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"fmt"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// onTick applies the liveness rule, sends the probe, settle reads and meter
// renewals when due, and runs the scene and re-read timers. Everything it
// sends is a read or a meter request; it never writes a parameter.
func (s *session) onTick(now time.Time) error {
	rx := s.c.lastRxAt()
	if err := s.liveness(now, rx); err != nil {
		return err
	}
	quiet := now.Sub(rx) >= s.timing.QuietProbeAfter && now.Sub(s.own.lastProbe) >= s.timing.QuietProbeAfter
	if quiet || now.Sub(s.own.lastProbe) >= s.timing.PingEvery {
		if err := s.c.writeData(wingproto.AppendValueRead(s.own.buf[:0], s.probeHash())); err != nil {
			return lost(err)
		}
		s.own.lastProbe = now
	}
	if err := s.sendSettleReads(now); err != nil {
		return err
	}
	if s.metersOn.Load() && now.Sub(s.own.lastMeter) >= s.timing.MeterRenewEvery {
		if err := s.c.writeMeters(wingproto.AppendMeterRenew(nil, s.meterID)); err != nil {
			return lost(err)
		}
		s.own.lastMeter = now
	}
	s.expireSent(now)
	s.expireReads(now)
	if err := s.sceneTick(now); err != nil {
		return err
	}
	return s.rereadDue(now)
}

// liveness applies the silence rules (review go M1): ConsoleSilent at
// StaleAfter, ConsoleResponding when messages return, teardown at
// DeadAfter or when the meter stream is silent too.
func (s *session) liveness(now, rx time.Time) error {
	silentFor := now.Sub(rx)
	if silentFor > s.timing.DeadAfter || (silentFor > s.timing.StaleAfter && s.metersSilent(now)) {
		return fmt.Errorf("wing: no message for %s: %w", silentFor.Round(time.Millisecond), driver.ErrConnectionLost)
	}
	switch {
	case silentFor > s.timing.StaleAfter && !s.silent.Load():
		s.silent.Store(true)
		s.dropPending() // never sent later (rule 3)
		s.log.Info("wing: console silent; input locked", "for", silentFor.Round(time.Millisecond))
		s.emit(driver.ConsoleSilent{Since: rx})
	case silentFor <= s.timing.StaleAfter && s.silent.Load():
		s.silent.Store(false)
		s.log.Info("wing: console answering again")
		s.emit(driver.ConsoleResponding{})
		return s.startReread(now) // read back what changed while we heard nothing
	}
	return nil
}

// metersSilent: meters are on and no packet of ours came for StaleAfter
// (counted from when they were switched on, if none came yet).
func (s *session) metersSilent(now time.Time) bool {
	if !s.metersOn.Load() || s.own.meterSince.IsZero() {
		return false
	}
	last := s.own.meterSince
	if at := s.link.meters.lastAt.Load(); at > last.UnixNano() {
		last = time.Unix(0, at)
	}
	return now.Sub(last) > s.timing.StaleAfter
}

// probeHash is /$syscfg/$firmware: a cheap leaf (wing.md 2.4).
func (s *session) probeHash() uint32 {
	if i, ok := s.tab.identity[pathFirmware]; ok {
		return s.tab.all[i].hash
	}
	return documentedFirmwareHash
}

// sendSettleReads reads back every parameter whose last write is
// SettleAfter old (wing.md 2.4, H2).
func (s *session) sendSettleReads(now time.Time) error {
	var req []byte
	for idx, at := range s.own.settleAt {
		if now.Before(at) {
			continue
		}
		delete(s.own.settleAt, idx)
		req = wingproto.AppendValueRead(req, s.tab.all[idx].hash)
		s.own.reads[idx] = append(s.own.reads[idx], readEntry{settle: true, at: now})
	}
	if len(req) == 0 {
		return nil
	}
	if err := s.c.writeData(req); err != nil {
		return lost(err)
	}
	return nil
}

// requestMeters starts the meter stream while meters are on (WG-F50).
func (s *session) requestMeters(now time.Time) error {
	if !s.metersOn.Load() {
		s.own.meterSince = time.Time{}
		return nil // the console stops after 5 s without a renewal (WG-F51)
	}
	req, err := s.link.meters.meterRequest(nil, s.meterID)
	if err != nil {
		s.log.Error("wing: meter request", "err", err)
		return nil
	}
	if err := s.c.writeMeters(req); err != nil {
		return lost(err)
	}
	if s.own.meterSince.IsZero() {
		s.own.meterSince = now
	}
	s.own.lastMeter = now
	return nil
}

// rereadState is the owner state of the background re-read.
type rereadState struct {
	wanted bool
	last   time.Time // when the last pass was sent
}

var (
	_ driver.Rereader          = (*session)(nil)
	_ driver.SelectiveRereader = (*session)(nil)
)

// Reread implements driver.Rereader. It returns at once.
func (s *session) Reread() error { return s.RereadExcept(nil) }

// RereadExcept implements driver.SelectiveRereader: one read-only pass
// without the held parameters. Passes run at most every RereadGap (TCP
// loses no pushes; the pass only guards against unpushed changes, item 5).
func (s *session) RereadExcept(skip []model.ParamRef) error {
	if s.stopped.Load() {
		return driver.ErrClosed
	}
	set := make(map[model.ParamRef]bool, len(skip))
	for _, p := range skip {
		set[p] = true
	}
	s.mu.Lock()
	s.rrSkip = set
	s.mu.Unlock()
	signal(s.rereadReady)
	return nil
}

// requestReread starts a pass now, or when the gap after the last passed.
func (s *session) requestReread(now time.Time) error {
	if !s.own.rr.last.IsZero() && now.Sub(s.own.rr.last) < s.timing.RereadGap {
		s.own.rr.wanted = true
		return nil
	}
	return s.startReread(now)
}

func (s *session) rereadDue(now time.Time) error {
	if s.own.rr.wanted && now.Sub(s.own.rr.last) >= s.timing.RereadGap {
		s.own.rr.wanted = false
		return s.startReread(now)
	}
	return nil
}

// startReread sends one data request per modelled parameter, except held
// ones, ones written within the echo window or awaiting a read-back, and
// AES50 headamps no channel uses. Replies that differ from what we know
// are console changes.
func (s *session) startReread(now time.Time) error {
	s.mu.Lock()
	skip := s.rrSkip
	s.rrSkip = nil
	s.mu.Unlock()
	s.own.rr.last = now
	var req []byte
	for idx, p := range s.tab.all {
		if s.skipReread(idx, &p, skip, now) {
			continue
		}
		req = wingproto.AppendValueRead(req, p.hash)
		if p.ref != (model.ParamRef{}) {
			s.own.reads[idx] = append(s.own.reads[idx], readEntry{at: now})
		}
	}
	if len(req) == 0 {
		return nil
	}
	if err := s.c.writeData(req); err != nil {
		return lost(err)
	}
	return nil
}

func (s *session) skipReread(idx int, p *param, skip map[model.ParamRef]bool, now time.Time) bool {
	h := s.own.sent[idx]
	_, settling := s.own.settleAt[idx]
	pendingRead := slices.ContainsFunc(s.own.reads[idx], func(r readEntry) bool { return r.settle })
	return s.absent[idx] || p.conv == convIdentity || skip[p.ref] || settling || pendingRead ||
		(len(h) > 0 && now.Sub(h[len(h)-1].at) <= s.timing.EchoWindow) ||
		(isAES(p.headamp) && !s.own.aesPresent[p.headamp])
}
