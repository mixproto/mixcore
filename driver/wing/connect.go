// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// documentedFirmwareHash is /$syscfg/$firmware (wing.md 2.4, section 3.5):
// the liveness probe when a schema lacks it.
const documentedFirmwareHash = 0x9E69815D

// connect reads the schema (or reuses the link's), then every modelled
// parameter. It sends definition and data requests only, never a value.
// It returns the walked schema for the link's cache (nil when reused).
func (s *session) connect(ctx context.Context, cache *schemaCache) (*schemaCache, error) {
	defs, reused, err := s.schema(ctx, cache)
	if err != nil {
		return nil, err
	}
	tab, err := buildTable(defs)
	if err != nil {
		return nil, s.schemaError(ctx, err)
	}
	s.tab = tab
	s.absent = make([]bool, len(tab.all))
	s.own = newOwnerState(tab)
	if err := s.readInitial(ctx); err != nil {
		return nil, err
	}
	if err := s.finishConnect(); err != nil {
		return nil, err
	}
	s.log.Info("wing: console identified", "model", s.ident.Model, "firmware", s.ident.Firmware, "inputs", tab.inputs,
		"schema_reused", reused)
	s.log.Debug("wing: console name", "name", s.ident.Name) // user content: Debug only
	if reused {
		return nil, nil
	}
	return &schemaCache{firmware: s.ident.Firmware, defs: defs}, nil
}

// schema reuses the cached definitions when the console still reports the
// same firmware string; otherwise it walks the tree.
func (s *session) schema(ctx context.Context, cache *schemaCache) (map[string]wingproto.Def, bool, error) {
	if cache == nil {
		defs, err := s.walk(ctx)
		return defs, false, err
	}
	if same, err := s.verifyCache(ctx, cache); err != nil {
		return nil, false, err
	} else if !same {
		s.log.Info("wing: the channel list differs from the cached schema; reading the schema again")
		defs, err := s.walk(ctx) // no data request was sent yet: no stray DE to drain
		return defs, false, err
	}
	hash := uint32(documentedFirmwareHash)
	if d, ok := cache.defs[pathFirmware]; ok {
		hash = d.Hash
	}
	fw, err := s.readString(ctx, hash)
	if err != nil {
		return nil, false, err
	}
	if fw == cache.firmware {
		return cache.defs, true, nil
	}
	s.log.Info("wing: firmware changed; reading the schema again", "was", cache.firmware, "now", fw)
	s.drain(ctx, s.timing.OptionalWait) // a DE after the data reply must not end a walk level (item 3)
	defs, err := s.walk(ctx)
	return defs, false, err
}

// verifyCache asks for the definitions of /ch once (review M2) and
// compares them with the cache: the same children, hashes and types. It
// runs before any data request, so a DE after data (item 3) cannot end it.
func (s *session) verifyCache(ctx context.Context, cache *schemaCache) (bool, error) {
	ch, ok := cache.defs["/ch"]
	if !ok {
		return false, nil
	}
	s.c.walking.Store(true)
	defer s.c.walking.Store(false)
	var budget walkBudget
	got, err := s.walkLevel(ctx, []walkNode{{path: "/ch", hash: ch.Hash}}, &budget)
	if err != nil {
		return false, err
	}
	cached := 0
	for path := range cache.defs {
		if strings.HasPrefix(path, "/ch/") && !strings.Contains(path[len("/ch/"):], "/") {
			cached++
		}
	}
	if len(got[0]) != cached {
		return false, nil
	}
	for _, d := range got[0] {
		c, ok := cache.defs["/ch/"+d.Name]
		if !ok || c.Hash != d.Hash || c.Type != d.Type {
			return false, nil
		}
	}
	return true, nil
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

// drain discards messages until wait passes without one.
func (s *session) drain(ctx context.Context, wait time.Duration) {
	for {
		if _, ok, err := s.next(ctx, wait); err != nil || !ok {
			return
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

// readSet sends one data request per parameter (D7 <hash> DC, WG-F31) and
// collects the values; pushes of the same parameters count as answers.
// Required parameters must answer within the connect timeout; the others
// get OptionalWait after the required ones and are absent if silent.
func (s *session) readSet(ctx context.Context, idxs []int) error {
	if len(idxs) == 0 {
		return nil
	}
	var req []byte
	left, required := map[int]bool{}, 0
	for _, i := range idxs {
		req = wingproto.AppendValueRead(req, s.tab.all[i].hash)
		left[i] = true
		if s.tab.all[i].required {
			required++
		}
	}
	if err := s.c.writeData(req); err != nil {
		return lost(err)
	}
	var optionalSince time.Time
	for len(left) > 0 {
		if required == 0 && optionalSince.IsZero() {
			optionalSince = time.Now()
		}
		wait := s.timing.ConnectTimeout
		if !optionalSince.IsZero() {
			if wait = s.timing.OptionalWait - time.Since(optionalSince); wait <= 0 {
				break
			}
		}
		m, ok, err := s.next(ctx, wait)
		if err != nil {
			return err
		}
		if !ok || m.kind != msgValue {
			continue
		}
		if i, known := s.tab.byHash[m.hash]; known && s.acceptInitial(i, m.val) && left[i] {
			delete(left, i)
			if s.tab.all[i].required {
				required--
			}
		}
	}
	for i := range left {
		s.absent[i] = true
		s.log.Info("wing: parameter did not answer; absent for this session", "path", s.tab.all[i].path)
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
