// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"context"
	"fmt"
	"time"

	"github.com/mixproto/mixcore/model"
)

// readState tracks the initial read over one or more passes.
type readState struct {
	values     []model.Value
	have       []bool
	sentAt     []time.Time
	tries      []int
	gaveUp     []bool // a parameter that may give up and did (feature or probe)
	probe      []bool // the one probe read of an optional input strip (XA-F72)
	inPass     []bool // asked for in the current pass
	sentInPass []bool
	order      []int // parameter indexes of the current pass, in send order
	next       int   // next position in order to request
	left       int   // parameters of the pass still without a value
	flight     int   // requested in this pass, not yet answered
}

func newReadState(n int) *readState {
	return &readState{values: make([]model.Value, n), have: make([]bool, n), sentAt: make([]time.Time, n),
		tries: make([]int, n), gaveUp: make([]bool, n), probe: make([]bool, n), inPass: make([]bool, n),
		sentInPass: make([]bool, n)}
}

// readAll reads every parameter of the strips this console has, with at most
// ReadWindow reads in flight, re-requesting unanswered ones. Pushes that
// arrive meanwhile count as answers.
//
// Optional input strips (XA-F72) cost a dead read per strip only on the
// first connect of a link: everything else is read first, then one probe per
// optional strip, then the rest of the strips that answered. Those strips are
// required from then on; a link remembers the count, so a reconnect reads
// exactly the strips that answered before and packet loss can only delay it,
// never drop a real strip.
func (s *session) readAll(ctx context.Context) (model.State, error) {
	l := s.params.layout
	rs := newReadState(len(s.params.all))
	switch {
	case s.knownInputs > 0:
		s.inputs = s.knownInputs
		if err := s.readPass(ctx, rs, s.plan(func(p param) bool { return p.strip <= s.inputs })); err != nil {
			return model.State{}, err
		}
	case l.RequiredInputs < l.Inputs:
		if err := s.readPass(ctx, rs, s.probePlan(rs)); err != nil {
			return model.State{}, err
		}
		s.inputs = s.probedInputs(rs)
		if err := s.readPass(ctx, rs, s.plan(func(p param) bool { return p.strip != 0 && p.strip <= s.inputs })); err != nil {
			return model.State{}, err
		}
	default:
		s.inputs = l.Inputs
		if err := s.readPass(ctx, rs, s.plan(func(param) bool { return true })); err != nil {
			return model.State{}, err
		}
	}
	s.known = rs.values
	s.absent = s.absentFeatures(rs)
	return s.buildState(rs.values)
}

// plan lists the parameters to read, in table order.
func (s *session) plan(keep func(param) bool) []int {
	var out []int
	for i, p := range s.params.all {
		if keep(p) {
			out = append(out, i)
		}
	}
	return out
}

// probePlan: every parameter that is not on an optional strip (inputs,
// main, groups, DCAs), then one fader read per optional strip, last, so dead
// reads never hold up the read window.
func (s *session) probePlan(rs *readState) []int {
	out := s.plan(func(p param) bool { return p.strip == 0 })
	for i, p := range s.params.all {
		if p.strip != 0 && p.ref.Field == model.FieldFader {
			rs.probe[i] = true
			out = append(out, i)
		}
	}
	return out
}

// probedInputs is the number of input strips below the first optional strip
// whose probe got no answer. The strips above it are absent until the engine
// session ends (a new link probes again); that is logged and reported as
// Capabilities.AbsentInputs.
func (s *session) probedInputs(rs *readState) int {
	l := s.params.layout
	n := l.Inputs
	for i, p := range s.params.all {
		if rs.probe[i] && rs.gaveUp[i] {
			n = min(n, p.strip-1)
		}
	}
	if n < l.Inputs {
		s.log.Warn(s.name+": input strips did not answer; absent until the engine is restarted",
			"inputs", n, "first absent", n+1, "last absent", l.Inputs)
	}
	return n
}

// readPass reads the parameters of order that have no value yet.
func (s *session) readPass(ctx context.Context, rs *readState, order []int) error {
	clear(rs.inPass)
	clear(rs.sentInPass)
	rs.order, rs.next, rs.left, rs.flight = order, 0, 0, 0
	for _, i := range order {
		rs.inPass[i] = true
		if !rs.have[i] {
			rs.left++
		}
	}
	for rs.left > 0 {
		s.requestMore(rs)
		p, ok, err := s.next(ctx, s.timing.ReadRetryEvery)
		if err != nil {
			return fmt.Errorf("initial read, %d of %d values missing: %w", rs.left, len(order), err)
		}
		if ok {
			s.acceptRead(rs, p)
		}
		s.retryReads(rs) // also while pushes keep arriving, or a lost reply is never re-requested
	}
	return nil
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
	for rs.flight < ReadWindow && rs.next < len(rs.order) {
		i := rs.order[rs.next]
		rs.next++
		if rs.have[i] {
			continue
		}
		s.sendSub(s.params.all[i].addr) // X32-F04: read = address without arguments
		rs.sentAt[i] = time.Now()
		rs.sentInPass[i] = true
		rs.tries[i]++
		rs.flight++
	}
}

func (s *session) retryReads(rs *readState) {
	now := time.Now()
	for _, i := range rs.order[:rs.next] {
		if rs.have[i] || !rs.sentInPass[i] || now.Sub(rs.sentAt[i]) < s.timing.ReadRetryEvery {
			continue
		}
		if (s.params.all[i].feature != featureCore || rs.probe[i]) && rs.tries[i] >= optionalReadTries {
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
