// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package x32

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
	// readWindow caps reads in flight during the initial read (X32-F07).
	readWindow = 16
	// optionalReadTries: a parameter of an optional feature that got no reply
	// to this many reads is taken as unknown to this console (X32-F06), and
	// its feature is absent for the session.
	optionalReadTries = 5
	// xinfoArgs is the number of string arguments of an /xinfo reply (X32-F10).
	xinfoArgs = 4
)

var (
	errNoReply     = errors.New("x32: console did not answer")
	errBadIdentity = errors.New("x32: malformed /xinfo reply")
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
	return nil
}

// capabilities of this console as far as it answered (X32-F35..F38).
func (s *session) capabilities() model.Capabilities {
	c := model.Capabilities{
		InputCount: inputChannels, HasMain: true, InputMeters: true,
		EchoesOwnWrites: true, MaxPushClients: maxPushClients,
	}
	if !s.absent[featureMuteGroups] {
		c.MuteGroups = muteGroupCount
	}
	if !s.absent[featureDCAs] {
		c.DCAs = dcaCount
	}
	return c
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
		return inPacket{}, false, fmt.Errorf("%w: %w", errNoReply, context.Cause(ctx))
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
			return parseXinfo(&s.msg)
		}
	}
}

// parseXinfo reads /xinfo ,ssss ip name model firmware (X32-F10).
func parseXinfo(m *osc.Message) (driver.Identity, error) {
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
	s.absent = s.absentFeatures(rs)
	return s.buildState(rs.values)
}

// absentFeatures lists the features with a parameter the console did not answer.
func (s *session) absentFeatures(rs *readState) map[feature]bool {
	out := map[feature]bool{}
	for i, p := range s.params.all {
		if rs.gaveUp[i] && !out[p.feature] {
			out[p.feature] = true
			s.log.Info("x32: console does not answer a parameter; feature off for this session", "addr", p.addr)
		}
	}
	return out
}

func (s *session) requestMore(rs *readState) {
	for rs.flight < readWindow && rs.next < len(rs.have) {
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
		if s.params.all[i].feature != featureCore && rs.tries[i] >= optionalReadTries {
			rs.have[i], rs.gaveUp[i] = true, true // unknown to this console (X32-F06)
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
	if osc.Decode(p.data, &s.msg) != nil {
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
	inputs := make([]model.Strip, inputChannels)
	for i := range inputs {
		inputs[i].Ref = model.StripRef{Kind: model.StripInput, Index: i + 1}
	}
	main := model.Strip{Ref: model.MainRef}
	st, err := model.NewState(inputs, &main)
	if err != nil {
		return model.State{}, err
	}
	st = st.WithGroups(groupCount(s.absent[featureMuteGroups], muteGroupCount), groupCount(s.absent[featureDCAs], dcaCount))
	for i, p := range s.params.all {
		if s.absent[p.feature] {
			continue
		}
		if st, err = st.With(p.ref, values[i]); err != nil {
			return model.State{}, fmt.Errorf("x32: %s: %w", p.addr, err)
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
