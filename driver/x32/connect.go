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
	return nil
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
	next   int // next parameter to request
	left   int // parameters still without a value
	flight int // requested, not yet answered
}

// readAll requests every parameter with at most readWindow in flight and
// re-requests unanswered ones. Pushes that arrive meanwhile count as answers.
func (s *session) readAll(ctx context.Context) (model.State, error) {
	n := len(s.params.all)
	rs := &readState{values: make([]model.Value, n), have: make([]bool, n), sentAt: make([]time.Time, n), left: n}
	for rs.left > 0 {
		s.requestMore(rs)
		p, ok, err := s.next(ctx, s.timing.ReadRetryEvery)
		if err != nil {
			return model.State{}, fmt.Errorf("initial read, %d of %d values missing: %w", rs.left, n, err)
		}
		if ok {
			s.acceptRead(rs, p)
			continue
		}
		s.retryReads(rs)
	}
	return s.buildState(rs.values)
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
		rs.flight++
	}
}

func (s *session) retryReads(rs *readState) {
	now := time.Now()
	for i := range rs.next {
		if !rs.have[i] && !rs.sentAt[i].IsZero() && now.Sub(rs.sentAt[i]) >= s.timing.ReadRetryEvery {
			s.sendSub(s.params.all[i].addr)
			rs.sentAt[i] = now
		}
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

func (s *session) buildState(values []model.Value) (model.State, error) {
	inputs := make([]model.Strip, inputChannels)
	main := model.Strip{Ref: model.MainRef}
	for i, p := range s.params.all {
		var err error
		if p.ref.Strip == model.MainRef {
			main, err = applyField(main, p.ref.Field, values[i])
		} else {
			k := p.ref.Strip.Index - 1
			inputs[k].Ref = p.ref.Strip
			inputs[k], err = applyField(inputs[k], p.ref.Field, values[i])
		}
		if err != nil {
			return model.State{}, err
		}
	}
	return model.NewState(inputs, &main)
}

func applyField(st model.Strip, f model.Field, v model.Value) (model.Strip, error) {
	switch f {
	case model.FieldFader:
		db, ok := v.DB()
		if !ok {
			return st, model.ErrInvalidValue
		}
		st.FaderDB = db
	case model.FieldMute:
		m, ok := v.Bool()
		if !ok {
			return st, model.ErrInvalidValue
		}
		st.Muted = m
	default:
		name, ok := v.Text()
		if !ok {
			return st, model.ErrInvalidValue
		}
		st.Name = name
	}
	return st, nil
}
