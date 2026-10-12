// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/model"
)

const (
	// ReadWindow caps reads in flight during the initial read and is the
	// batch size of a background re-read step (X32-F07, XA-F05).
	ReadWindow = 16
	// optionalReadTries: a parameter of an optional feature that got no reply
	// to this many reads is taken as unknown to this console (X32-F06), and
	// its feature is absent for the session.
	optionalReadTries = 5
	// xinfoArgs is the number of string arguments of an /xinfo reply (X32-F10).
	xinfoArgs = 4
)

var (
	// ErrNoReply: the console did not answer /xinfo or the initial read in time.
	ErrNoReply     = errors.New("console did not answer")
	errBadIdentity = errors.New("malformed /xinfo reply")
)

// connect identifies the console, registers for pushes and reads the full
// state. It sends only /xinfo, /xremote and parameter reads.
func (s *session) connect(ctx context.Context) error {
	ident, err := s.identify(ctx)
	if err != nil {
		return err
	}
	s.ident = ident
	// Model and firmware are not user content; the device name is (Debug only).
	// No firmware is refused: absent features stay absent (XA section 1).
	s.log.Info(s.name+": console identified", "model", ident.Model, "firmware", ident.Firmware)
	s.log.Debug(s.name+": console name", "name", ident.Name)
	s.sendSub(addrXremote) // before the read, so changes during the read are not missed (X32-F20)
	state, err := s.readAll(ctx)
	if err != nil {
		return err
	}
	s.snapshot = state
	s.caps = s.capabilities()
	s.c.meters.SetAbsent(s.inputs, s.params.layout.Inputs) // the frame keeps its size; absent inputs read -inf
	s.lastPong = time.Now()                                // the answered reads prove the uplink as well as an /xinfo reply
	return nil
}

// capabilities of this console as far as it answered (X32-F35..F38, XA-F72).
func (s *session) capabilities() model.Capabilities {
	l := s.params.layout
	c := model.Capabilities{
		InputCount: s.inputs, AbsentInputs: l.Inputs - s.inputs, HasMain: true, InputMeters: true,
		EchoesOwnWrites: true, MaxPushClients: s.prof.MaxPushClients,
	}
	if !s.absent[featureMuteGroups] {
		c.MuteGroups = l.MuteGroups
		c.MainMuteGroups = l.MainMuteGroups
	}
	if !s.absent[featureDCAs] {
		c.DCAs = l.DCAs
	}
	return c
}

// isAbsent reports whether p belongs to a feature or an input strip this
// console did not answer for (never faked).
func (s *session) isAbsent(p param) bool {
	return s.absent[p.feature] || p.ref.Strip.Kind == model.StripInput && p.ref.Strip.Index > s.inputs
}

// next waits up to wait for a packet. ok is false on timeout.
func (s *session) next(ctx context.Context, wait time.Duration) (inPacket, bool, error) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case p := <-s.c.inbound:
		return p, true, nil
	case err := <-s.c.errs:
		return inPacket{}, false, fmt.Errorf("%w: %w", driver.ErrConnectionLost, err)
	case <-t.C:
		return inPacket{}, false, nil
	case <-s.done:
		return inPacket{}, false, driver.ErrClosed
	case <-ctx.Done():
		return inPacket{}, false, fmt.Errorf("%w: %w", ErrNoReply, context.Cause(ctx))
	}
}

func (s *session) identify(ctx context.Context) (driver.Identity, error) {
	for {
		s.sendSub(addrXinfo)
		deadline := time.Now().Add(s.timing.ReadRetryEvery)
		for wait := time.Until(deadline); wait > 0; wait = time.Until(deadline) {
			p, ok, err := s.next(ctx, wait)
			if err != nil {
				return driver.Identity{}, err
			}
			if !ok || osc.Decode(p.data, &s.msg) != nil || !s.msg.AddressIs(addrXinfo) {
				continue
			}
			s.lastPong = p.at
			return ParseXinfo(&s.msg)
		}
	}
}

// ParseXinfo reads /xinfo ,ssss ip name model firmware (X32-F10, XA-F10).
func ParseXinfo(m *osc.Message) (driver.Identity, error) {
	var f [xinfoArgs]string
	for i := range f {
		v, ok := m.StringArg(i)
		if !ok {
			return driver.Identity{}, errBadIdentity
		}
		f[i] = nameFromWire([]byte(v))
	}
	return driver.Identity{Address: f[0], Name: f[1], Model: f[2], Firmware: f[3]}, nil
}

func (s *session) acceptRead(rs *readState, p inPacket) {
	if p.viaCmd || osc.Decode(p.data, &s.msg) != nil { // viaCmd: a late re-read reply of an earlier session
		return
	}
	par, ok := s.params.byAddress(s.msg.Address)
	if !ok {
		return
	}
	arg, ok := s.msg.Arg(0)
	if !ok {
		return
	}
	v, err := fromWire(par, arg)
	if err != nil {
		return
	}
	i := s.params.index(par.ref)
	rs.values[i] = v
	if rs.have[i] {
		return
	}
	rs.have[i] = true
	if !rs.inPass[i] {
		return // a push of a parameter a later pass would have asked for
	}
	rs.left--
	if rs.sentInPass[i] {
		rs.flight--
	}
}

// buildState makes the snapshot from the values read. Parameters of absent
// features are left out: a missing feature is absent, never faked.
func (s *session) buildState(values []model.Value) (model.State, error) {
	inputs := make([]model.Strip, s.inputs)
	for i := range inputs {
		inputs[i].Ref = model.StripRef{Kind: model.StripInput, Index: i + 1}
	}
	main := model.Strip{Ref: model.MainRef}
	st, err := model.NewState(inputs, &main)
	if err != nil {
		return model.State{}, err
	}
	l := s.params.layout
	st = st.WithGroups(groupCount(s.absent[featureMuteGroups], l.MuteGroups), groupCount(s.absent[featureDCAs], l.DCAs))
	for i, p := range s.params.all {
		if s.isAbsent(p) {
			continue
		}
		if st, err = st.With(p.ref, values[i]); err != nil {
			return model.State{}, fmt.Errorf("%s: %s: %w", s.name, p.addr, err)
		}
	}
	return st, nil
}

func groupCount(absent bool, n int) int {
	if absent {
		return 0
	}
	return n
}
