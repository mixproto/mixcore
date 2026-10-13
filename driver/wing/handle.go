// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// maxEchoHistory is how many recent writes per parameter are matched
// against pushes, so the echo of an earlier write in a fast drag is still
// recognised (an unrecognised echo would end the user's own gesture).
const maxEchoHistory = 32

// consoleOrigin is the origin of every change the console reports.
var consoleOrigin = driver.Origin{Kind: driver.OriginConsole}

// onMessage handles one message while running. Only values matter now:
// definitions and DE tokens belong to the connect phase.
func (s *session) onMessage(m inMsg) {
	if m.kind != msgValue {
		return
	}
	idx, ok := s.tab.byHash[m.hash]
	if !ok || s.absent[idx] {
		return // the probe's reply, derived $ values (WG-F24) or anything else we did not model
	}
	p := &s.tab.all[idx]
	switch p.conv {
	case convTCPLock:
		s.onLock(m.val)
		return
	case convDCAMgrp:
		s.onDCAMgrp(m.val)
		return
	case convSrcGroup, convSrcIndex:
		s.onSource(p, m.val, m.at)
		return
	case convIdentity:
		return
	}
	if isAES(p.headamp) && !s.own.aesPresent[p.headamp] {
		return // no channel uses it: not in the state (x32 rule for patched headamps)
	}
	v, err := s.fromWire(p, m.val)
	if err != nil {
		s.log.Debug("wing: value outside the documented encoding", "path", p.path, "err", err)
		return
	}
	if ev, ok := s.classify(idx, p, v, m.val, m.at); ok {
		s.emit(ev)
	}
}

// classify tells a read reply, our echo and a console change apart.
func (s *session) classify(idx int, p *param, v model.Value, w wireValue, at time.Time) (driver.ParamChanged, bool) {
	ev := driver.ParamChanged{Param: p.ref, Value: v, Origin: consoleOrigin, At: at}
	if q := s.own.reads[idx]; len(q) > 0 {
		r := q[0]
		s.popRead(idx)
		switch {
		case r.superseded:
			return ev, false // asked before our newer write: may be older than it
		case r.settle:
			return s.settleReply(idx, p, v, w, ev), true
		case s.own.have[idx] && s.own.known[idx].Equal(v):
			return ev, false // a background re-read found nothing new
		}
		s.remember(idx, v)
		return ev, true // a change the console did not push (item 5)
	}
	s.remember(idx, v)
	if o, ok := s.matchEcho(idx, p, w, at); ok {
		ev.Origin = o // hypothesis H1 of wing.md 2.4: the console pushed our write back
		return ev, true
	}
	s.onConsolePush(idx, at)
	return ev, true
}

// settleReply is the read-back after our write (H2 of wing.md 2.4): the
// console's value now. Authoritative ends every Gain value in flight;
// OwnWrite (the last value we sent, within half a step) never ends a hold.
func (s *session) settleReply(idx int, p *param, v model.Value, w wireValue, ev driver.ParamChanged) driver.ParamChanged {
	last, wrote := s.own.lastSent[idx]
	delete(s.own.lastSent, idx)
	ev.Authoritative = true
	ev.OwnWrite = wrote && sameWire(p, last, w)
	s.remember(idx, v)
	return ev
}

// matchEcho returns the origin of our own recent write with the same value.
// The matched write and everything older are forgotten.
func (s *session) matchEcho(idx int, p *param, w wireValue, at time.Time) (driver.Origin, bool) {
	h := s.own.sent[idx]
	for i := len(h) - 1; i >= 0; i-- {
		if at.Sub(h[i].at) > s.timing.EchoWindow || !sameWire(p, h[i].val, w) {
			continue
		}
		origin := h[i].origin
		if rest := h[i+1:]; len(rest) > 0 {
			s.own.sent[idx] = rest
		} else {
			delete(s.own.sent, idx)
		}
		return origin, true
	}
	return driver.Origin{}, false
}

func (s *session) popRead(idx int) {
	if q := s.own.reads[idx]; len(q) > 1 {
		s.own.reads[idx] = q[1:]
		return
	}
	delete(s.own.reads, idx)
}

// onLock follows /$syscfg/tcplock (WG-F26): while 1, Apply refuses writes.
func (s *session) onLock(w wireValue) {
	on, err := switchFromWire(w)
	if err != nil {
		return
	}
	if s.locked.Swap(on) != on {
		s.log.Info("wing: TCP remote lock changed", "locked", on)
	}
}

// onDCAMgrp follows /cfg/dcamgrp (wing.md 3.2): it changes the rule (a
// muted DCA mutes its members or not), never the membership.
func (s *session) onDCAMgrp(w wireValue) {
	on, err := switchFromWire(w)
	if err != nil || on == s.own.dcamgrp {
		return
	}
	s.own.dcamgrp = on
	s.emit(driver.MuteRulesChanged{Rules: s.muteRules()})
}

// onSource follows a channel's source (wing.md 3.4): a new headamp is
// reported on the input, and an AES50 headamp no channel used before is
// read now (a read, never a write).
func (s *session) onSource(p *param, w wireValue, at time.Time) {
	n := p.input
	if p.conv == convSrcGroup {
		name, err := enumNameFromWire(w, p.items)
		if err != nil {
			return
		}
		s.own.srcGroup[n-1] = name
	} else {
		v, err := intFromWire(w, 1, maxSourceIndex)
		if err != nil {
			return
		}
		s.own.srcIndex[n-1] = v
	}
	h := s.visibleHeadamp(n)
	if h == s.own.headampOf[n-1] || !s.caps.InputHeadamps {
		return
	}
	s.own.headampOf[n-1] = h
	if def, ok := s.tab.headamp(h); ok && isAES(h) && !s.own.aesPresent[h] {
		s.own.aesPresent[h] = true
		s.readParams(def.gain, def.phantom)
	}
	v, _ := model.Index(h)
	s.emit(driver.ParamChanged{Param: pref(model.StripInput, n, model.FieldHeadamp), Value: v, Origin: consoleOrigin, At: at})
}

// readParams sends data requests whose replies are reported like a
// background re-read (a value new to the state is a change).
func (s *session) readParams(idxs ...int) {
	var req []byte
	for _, i := range idxs {
		if i < 0 || s.absent[i] {
			continue
		}
		req = wingproto.AppendValueRead(req, s.tab.all[i].hash)
		s.own.reads[i] = append(s.own.reads[i], readEntry{at: time.Now()})
	}
	if len(req) > 0 {
		if err := s.c.writeData(req); err != nil {
			s.log.Debug("wing: read failed", "err", err) // the reader reports the broken connection
		}
	}
}

// flushPending sends every queued write: D7 <hash> <value> (WG-F31). A
// failed send ends the session; nothing is retried or replayed.
func (s *session) flushPending() error {
	now := time.Now()
	for _, w := range s.takePending() {
		p := &s.tab.all[w.idx]
		buf, err := appendValue(wingproto.AppendHash(s.own.buf[:0], p.writeHash), w.val)
		if err != nil {
			s.log.Error("wing: encode write", "path", p.path, "err", err)
			continue
		}
		s.own.buf = buf
		if err := s.c.writeData(buf); err != nil {
			return lost(err)
		}
		s.rememberSent(w.idx, sentWrite{val: w.val, origin: w.origin, at: now})
		s.own.lastSent[w.idx] = w.val
		s.own.settleAt[w.idx] = now.Add(s.timing.SettleAfter)
		for i := range s.own.reads[w.idx] {
			s.own.reads[w.idx][i].superseded = true
		}
		if s.audit != nil {
			s.audit(driver.WriteRecord{Param: p.ref, Address: p.writePath, Origin: w.origin, At: now})
		}
	}
	return nil
}

func (s *session) rememberSent(idx int, w sentWrite) {
	h := append(s.own.sent[idx], w)
	if len(h) > maxEchoHistory {
		h = h[len(h)-maxEchoHistory:]
	}
	s.own.sent[idx] = h
}

// expireSent forgets writes older than the echo window.
func (s *session) expireSent(now time.Time) {
	for idx, h := range s.own.sent {
		if now.Sub(h[len(h)-1].at) > s.timing.EchoWindow {
			delete(s.own.sent, idx)
		}
	}
}
