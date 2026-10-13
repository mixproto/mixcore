// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

var errRefused = errors.New("fake: console unreachable")

// fakeDriver hands out scripted sessions. Each Connect either fails (while
// down is true) or returns a new fakeSession built from the current snapshot.
type fakeDriver struct {
	mu        sync.Mutex
	down      bool
	flaky     bool // sessions are lost right after connect
	snapshot  model.State
	caps      model.Capabilities
	law       driver.FaderLaw
	selective bool // sessions implement driver.SelectiveRereader
	sessions  []*fakeSession
	connectAt []time.Time
}

func (d *fakeDriver) Family() driver.Family     { return "fake" }
func (d *fakeDriver) FaderLaw() driver.FaderLaw { return d.law }

func (d *fakeDriver) Connect(ctx context.Context, _ driver.Endpoint, _ driver.Options) (driver.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connectAt = append(d.connectAt, time.Now())
	if d.down {
		return nil, errRefused
	}
	s := &fakeSession{snapshot: d.snapshot, caps: d.caps, lost: make(chan struct{}), push: make(chan driver.Event, 16)}
	d.sessions = append(d.sessions, s)
	if d.flaky {
		s.lose()
	}
	if d.selective {
		return selectiveSession{s}, nil
	}
	return s, nil
}

func (d *fakeDriver) setDown(down bool) {
	d.mu.Lock()
	d.down = down
	d.mu.Unlock()
}

func (d *fakeDriver) setSnapshot(st model.State) {
	d.mu.Lock()
	d.snapshot = st
	d.mu.Unlock()
}

func (d *fakeDriver) session(i int) *fakeSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i >= len(d.sessions) {
		return nil
	}
	return d.sessions[i]
}

func (d *fakeDriver) attempts() []time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Time(nil), d.connectAt...)
}

type fakeSession struct {
	snapshot model.State
	caps     model.Capabilities
	lost     chan struct{}
	lostOnce sync.Once
	push     chan driver.Event

	mu      sync.Mutex
	applied []driver.Command
	closed  bool
	meters  driver.MeterRequest
	rereads int
	skips   [][]model.ParamRef // RereadExcept calls (selective sessions)
	failing error              // Apply returns it (a send that failed)
	onApply func()             // called at the start of Apply (tests of lock order)
}

// failApplies makes Apply return err (nil: succeed again).
func (s *fakeSession) failApplies(err error) {
	s.mu.Lock()
	s.failing = err
	s.mu.Unlock()
}

// selectiveSession is a fakeSession that can leave held parameters out of a re-read.
type selectiveSession struct{ *fakeSession }

func (s selectiveSession) RereadExcept(skip []model.ParamRef) error {
	s.mu.Lock()
	s.skips = append(s.skips, skip)
	s.mu.Unlock()
	return s.Reread()
}

func (s *fakeSession) Identity() driver.Identity        { return driver.Identity{Name: "fake"} }
func (s *fakeSession) Capabilities() model.Capabilities { return s.caps }
func (s *fakeSession) Snapshot() model.State            { return s.snapshot }

func (s *fakeSession) Run(ctx context.Context, events chan<- driver.Event) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.lost:
			return driver.ErrConnectionLost
		case ev := <-s.push:
			select {
			case events <- ev:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (s *fakeSession) Apply(cmd driver.Command) error {
	if !cmd.Origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if s.onApply != nil {
		s.onApply()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return driver.ErrClosed
	}
	if s.failing != nil {
		return s.failing
	}
	s.applied = append(s.applied, cmd)
	return nil
}

func (s *fakeSession) SetMeters(req driver.MeterRequest) error {
	s.mu.Lock()
	s.meters = req
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.lose()
	return nil
}

func (s *fakeSession) Reread() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return driver.ErrClosed
	}
	s.rereads++
	return nil
}

func (s *fakeSession) rereadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rereads
}

func (s *fakeSession) lose() { s.lostOnce.Do(func() { close(s.lost) }) }

func (s *fakeSession) writes() []driver.Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]driver.Command(nil), s.applied...)
}

func stateWith(faderDB float64) model.State {
	inputs := []model.Strip{{Ref: model.StripRef{Kind: model.StripInput, Index: 1}, FaderDB: faderDB}}
	main := model.Strip{Ref: model.MainRef, FaderDB: math.Inf(-1)}
	st, err := model.NewState(inputs, &main)
	if err != nil {
		panic(err)
	}
	return st
}
