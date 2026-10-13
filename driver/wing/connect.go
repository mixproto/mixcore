// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// documentedFirmwareHash is /$syscfg/$firmware (wing.md 2.4, section 3.5):
// the liveness probe when a schema lacks it.
const documentedFirmwareHash = 0x9E69815D

// connect walks the schema (every time: no schema outlives its session,
// security re-check 1), then reads every modelled parameter. It sends
// definition and data requests only, never a value.
func (s *session) connect(ctx context.Context) error {
	defs, err := s.walk(ctx)
	if err != nil {
		return err
	}
	tab, err := buildTable(defs)
	if err != nil {
		return s.schemaError(ctx, err)
	}
	s.tab = tab
	s.absent = make([]bool, len(tab.all))
	s.own = newOwnerState(tab)
	if err := s.readInitial(ctx); err != nil {
		return err
	}
	if err := s.finishConnect(); err != nil {
		return err
	}
	s.log.Info("wing: console identified", "model", s.ident.Model, "firmware", s.ident.Firmware, "inputs", tab.inputs)
	s.log.Debug("wing: console name", "name", s.ident.Name) // user content: Debug only
	return nil
}

// schemaError names the console's firmware in a schema error: firmware
// below 2.0 has another definition layout (WG-F05) and is not supported.
func (s *session) schemaError(ctx context.Context, err error) error {
	ctx, cancel := context.WithTimeout(ctx, s.timing.WalkQuiet)
	defer cancel()
	fw, ferr := s.readString(ctx, documentedFirmwareHash)
	if ferr != nil {
		fw = "unknown"
	}
	return fmt.Errorf("%w (console firmware %q; firmware below 2.0 is not supported, WG-F05)", err, fw)
}

// readString reads one string leaf by hash.
func (s *session) readString(ctx context.Context, hash uint32) (string, error) {
	if err := s.c.writeData(wingproto.AppendValueRead(nil, hash)); err != nil {
		return "", lost(err)
	}
	for {
		m, ok, err := s.next(ctx, s.timing.ConnectTimeout)
		if err != nil {
			return "", err
		}
		if ok && m.kind == msgValue && m.hash == hash && m.val.kind == wireText {
			return m.val.s, nil
		}
	}
}

// readInitial reads every parameter except AES50 headamps, then the AES50
// headamps that a channel uses (their sources are known only then).
func (s *session) readInitial(ctx context.Context) error {
	var first []int
	for i, p := range s.tab.all {
		if !isAES(p.headamp) {
			first = append(first, i)
		}
	}
	if err := s.readSet(ctx, first); err != nil {
		return err
	}
	s.applyInternals()
	var second []int
	for _, h := range s.own.headampOf {
		if def, ok := s.tab.headamp(h); ok && isAES(h) && !s.own.aesPresent[h] {
			s.own.aesPresent[h] = true
			second = append(second, def.gain)
			if def.phantom >= 0 {
				second = append(second, def.phantom)
			}
		}
	}
	return s.readSet(ctx, second)
}

func isAES(headamp int) bool { return headamp > aesAOffset }

// Read retries (review go M2): a console may drop requests beyond its
// pipeline (item 3), so unanswered reads are asked again in small batches.
const (
	readRetryRounds = 3
	readBatch       = 4
)

// readSet sends one data request per parameter (D7 <hash> DC, WG-F31) and
// collects the values; pushes of the same parameters count as answers.
// Reads still unanswered are asked again, readBatch at a time, and one at
// a time once a batch comes back incomplete, for readRetryRounds rounds. Then a required parameter fails the connect
// (ErrNoReply) and any other is absent for the session.
func (s *session) readSet(ctx context.Context, idxs []int) error {
	if len(idxs) == 0 {
		return nil
	}
	left := make(map[int]bool, len(idxs))
	for _, i := range idxs {
		left[i] = true
	}
	if err := s.askValues(ctx, idxs, left); err != nil {
		return err
	}
	batch := readBatch
	for round := 0; round < readRetryRounds && len(left) > 0; round++ {
		missing := slices.Sorted(maps.Keys(left))
		for start := 0; start < len(missing); start += batch {
			which := missing[start:min(start+batch, len(missing))]
			if err := s.askValues(ctx, which, left); err != nil {
				return err
			}
			if slices.ContainsFunc(which, func(i int) bool { return left[i] }) {
				batch = 1 // this console drops pipelined requests: one at a time
			}
		}
	}
	for _, i := range slices.Sorted(maps.Keys(left)) {
		if s.tab.all[i].required {
			return fmt.Errorf("%w: %s", ErrNoReply, s.tab.all[i].path)
		}
		s.absent[i] = true
		s.log.Info("wing: parameter did not answer; absent for this session", "path", s.tab.all[i].path)
	}
	return nil
}

// askValues reads the parameters in which and collects answers until each
// is answered or OptionalWait passes without a message.
func (s *session) askValues(ctx context.Context, which []int, left map[int]bool) error {
	var req []byte
	for _, i := range which {
		req = wingproto.AppendValueRead(req, s.tab.all[i].hash)
	}
	if err := s.c.writeData(req); err != nil {
		return lost(err)
	}
	for slices.ContainsFunc(which, func(i int) bool { return left[i] }) {
		m, ok, err := s.next(ctx, s.timing.OptionalWait)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if m.kind != msgValue {
			continue
		}
		if i, known := s.tab.byHash[m.hash]; known && s.acceptInitial(i, m.val) {
			delete(left, i)
		}
	}
	return nil
}

// acceptInitial stores a value read during connect; false when it does not
// fit the parameter's encoding (it is then asked again by nobody: absent).
func (s *session) acceptInitial(i int, w wireValue) bool {
	p := &s.tab.all[i]
	switch p.conv {
	case convSrcGroup, convSrcIndex, convDCAMgrp, convTCPLock, convIdentity:
		s.own.raw[i] = w
		return true
	}
	v, err := s.fromWire(p, w)
	if err != nil {
		s.log.Debug("wing: value outside the documented encoding", "path", p.path, "err", err)
		return false
	}
	s.remember(i, v)
	return true
}

// applyInternals interprets the internal parameters read during connect.
func (s *session) applyInternals() {
	for i, p := range s.tab.all {
		w := s.own.raw[i]
		if w.kind == wireNone {
			continue
		}
		switch p.conv {
		case convDCAMgrp:
			if on, err := switchFromWire(w); err == nil {
				s.own.dcamgrp = on
			}
		case convTCPLock:
			if on, err := switchFromWire(w); err == nil {
				s.locked.Store(on)
			}
		case convSrcGroup:
			s.own.srcGroup[p.input-1], _ = enumNameFromWire(w, p.items)
		case convSrcIndex:
			s.own.srcIndex[p.input-1], _ = intFromWire(w, 1, maxSourceIndex)
		}
	}
	s.limitLocalHeadamps()
	for n := 1; n <= s.tab.inputs; n++ {
		s.own.headampOf[n-1] = s.visibleHeadamp(n)
	}
}

// limitLocalHeadamps makes LCL headamps above the model's local inputs
// absent: 8 on WING, 24 on Rack and Compact (WG-F03, WG-F04).
func (s *session) limitLocalHeadamps() {
	local := localInputsFor(s.identity(pathModel))
	for _, h := range s.tab.headamps {
		if h.bank == model.BankLocal && h.number > local {
			s.absent[h.gain] = true
			if h.phantom >= 0 {
				s.absent[h.phantom] = true
			}
		}
	}
}

// identity returns an identity string read during connect.
func (s *session) identity(path string) string {
	if i, ok := s.tab.identity[path]; ok && s.own.raw[i].kind == wireText {
		return s.own.raw[i].s
	}
	return ""
}

// finishConnect builds the identity, capabilities and snapshot.
func (s *session) finishConnect() error {
	s.ident.Address = s.link.addr
	s.ident.Model = s.identity(pathModel)
	s.ident.Firmware = s.identity(pathFirmware)
	s.ident.Name = s.identity(pathName)
	if s.locked.Load() {
		s.log.Info("wing: the console is locked for TCP remote control; writes are refused")
	}
	s.caps = s.capabilities()
	st, err := s.buildState()
	if err != nil {
		return err
	}
	s.snapshot = st
	return nil
}
