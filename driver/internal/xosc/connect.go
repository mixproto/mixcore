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
	s.sendSub(addrXremote) // before the read, so changes during the read are not missed (X32-F20)
	state, err := s.readAll(ctx)
	if err != nil {
		return err
	}
	s.snapshot = state
	s.caps = s.capabilities()
	s.lastPong = time.Now() // the answered reads prove the uplink as well as an /xinfo reply
	return nil
}

// capabilities of this console as far as it answered (X32-F35..F38, XA-F72).
func (s *session) capabilities() model.Capabilities {
	l := s.params.layout
	c := model.Capabilities{
		InputCount: s.inputs, HasMain: true, InputMeters: true,
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

// readState tracks the initial read.
type readState struct {
	values []model.Value
	have   []bool
	sentAt []time.Time
	tries  []int
	gaveUp []bool // optional parameter the console did not answer
	next   int    // next parameter to request
	left   int    // parameters still without a value
	flight int    // requested, not yet answered
}

// readAll requests every parameter with at most readWindow in flight and
// re-requests unanswered ones. Pushes that arrive meanwhile count as answers.
func (s *session) readAll(ctx context.Context) (model.State, error) {
	n := len(s.params.all)
	rs := &readState{values: make([]model.Value, n), have: make([]bool, n), sentAt: make([]time.Time, n),
		tries: make([]int, n), gaveUp: make([]bool, n), left: n}
	for rs.left > 0 {
		s.requestMore(rs)
		p, ok, err := s.next(ctx, s.timing.ReadRetryEvery)
		if err != nil {
			return model.State{}, fmt.Errorf("initial read, %d of %d values missing: %w", rs.left, n, err)
		}
		if ok {
			s.acceptRead(rs, p)
		}
		s.retryReads(rs) // also while pushes keep arriving, or a lost reply is never re-requested
	}
	s.known = rs.values
	s.inputs = s.answeredInputs(rs)
	s.absent = s.absentFeatures(rs)
	return s.buildState(rs.values)
}

// answeredInputs is the number of input strips that answered: the inputs
// below the first optional strip with an unanswered name, fader or mute
// (XA-F72). Strips above it are absent for the session, so the model's
// inputs stay numbered 1..n.
func (s *session) answeredInputs(rs *readState) int {
	n := s.params.layout.Inputs
	for i, p := range s.params.all {
		if rs.gaveUp[i] && p.strip != 0 && p.feature == featureCore {
			n = min(n, p.strip-1)
		}
	}
	if n < s.params.layout.Inputs {
		s.log.Info(s.name+": console does not answer some input strips; they are absent for this session", "inputs", n)
	}
	return n
}

// absentFeatures lists the features with a parameter the console did not
// answer. Parameters of absent input strips do not count: a missing strip
// must not switch mute groups or DCAs off for the strips that exist.
func (s *session) absentFeatures(rs *readState) map[feature]bool {
	out := map[feature]bool{}
	for i, p := range s.params.all {
		if !rs.gaveUp[i] || out[p.feature] || p.feature == featureCore || p.strip > s.inputs {
			continue
		}
		out[p.feature] = true
		s.log.Info(s.name+": console does not answer a parameter; feature off for this session", "addr", p.addr)
	}
	return out
}

func (s *session) requestMore(rs *readState) {
	for rs.flight < ReadWindow && rs.next < len(rs.have) {
		i := rs.next
		rs.next++
		if rs.have[i] {
			continue
		}
		s.sendSub(s.params.all[i].addr) // X32-F04: read = address without arguments
		rs.sentAt[i] = time.Now()
		rs.tries[i]++
		rs.flight++
	}
}

func (s *session) retryReads(rs *readState) {
	now := time.Now()
	for i := range rs.next {
		if rs.have[i] || rs.sentAt[i].IsZero() || now.Sub(rs.sentAt[i]) < s.timing.ReadRetryEvery {
			continue
		}
		if s.params.all[i].optional() && rs.tries[i] >= optionalReadTries {
			rs.have[i], rs.gaveUp[i] = true, true // unknown to this console (X32-F06, XA-F72)
			rs.left--
			rs.flight--
			continue
		}
		s.sendSub(s.params.all[i].addr)
		rs.sentAt[i] = now
		rs.tries[i]++
	}
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
	rs.left--
	if !rs.sentAt[i].IsZero() {
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
