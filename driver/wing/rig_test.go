// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	consoleIP = "10.0.0.1"
	clientIP  = "10.0.0.2"
)

var gesture = driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 1}

// pipeEnd is one end of an in-memory TCP connection with real addresses.
type pipeEnd struct {
	net.Conn
	local, remote net.Addr
}

func (p pipeEnd) LocalAddr() net.Addr  { return p.local }
func (p pipeEnd) RemoteAddr() net.Addr { return p.remote }

// rig is a simulator and the driver: TCP over net.Pipe (Options.Dial), UDP
// over memnet, inside a synctest bubble.
type rig struct {
	t     *testing.T
	net   *memnet.Network
	sim   *simwing.Sim
	drv   *Driver
	slot  *meters.Slot
	ctx   context.Context
	wg    sync.WaitGroup
	mu    sync.Mutex
	audit []driver.WriteRecord
	dials int
}

func newRig(t *testing.T, o simwing.Options) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, net: memnet.New(), sim: simwing.New(o), drv: New(Config{}), slot: &meters.Slot{}, ctx: ctx}
	pc, err := r.net.ListenPacket(consoleIP + ":2222")
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Go(func() { _ = r.sim.ServeUDP(ctx, pc) })
	t.Cleanup(func() { cancel(); r.sim.DropConnections(); r.wg.Wait(); _ = pc.Close() })
	return r
}

func (r *rig) dial(_ context.Context, _, _ string) (net.Conn, error) {
	r.mu.Lock()
	r.dials++
	r.mu.Unlock()
	client, server := net.Pipe()
	console := &net.TCPAddr{IP: net.ParseIP(consoleIP), Port: DefaultPort}
	me := &net.TCPAddr{IP: net.ParseIP(clientIP), Port: 50000}
	r.wg.Go(func() { r.sim.ServeConn(r.ctx, pipeEnd{Conn: server, local: console, remote: me}) })
	return pipeEnd{Conn: client, local: me, remote: console}, nil
}

func (r *rig) opts() driver.Options {
	return driver.Options{
		Dial: r.dial, ListenPacket: r.net.Host(clientIP), Meters: r.slot,
		WriteAudit: func(w driver.WriteRecord) { r.mu.Lock(); r.audit = append(r.audit, w); r.mu.Unlock() },
	}
}

func (r *rig) audits() []driver.WriteRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]driver.WriteRecord(nil), r.audit...)
}

func (r *rig) connect() *session {
	r.t.Helper()
	sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, r.opts())
	if err != nil {
		r.t.Fatalf("Connect: %v", err)
	}
	r.t.Cleanup(func() { _ = sess.Close() })
	return sess.(*session)
}

// running is a session whose Run loop goes on in the background.
type running struct {
	s      *session
	events chan driver.Event
	done   chan error
	cancel context.CancelFunc
}

func (r *rig) run(s *session) *running {
	ctx, cancel := context.WithCancel(context.Background())
	rn := &running{s: s, events: make(chan driver.Event, 1024), done: make(chan error, 1), cancel: cancel}
	r.wg.Go(func() { rn.done <- s.Run(ctx, rn.events) })
	r.t.Cleanup(func() { cancel(); <-rn.done; rn.done <- nil })
	return rn
}

// changes drains the events that arrived within d.
func (rn *running) changes(d time.Duration) []driver.ParamChanged {
	time.Sleep(d)
	synctest.Wait()
	var out []driver.ParamChanged
	for {
		select {
		case ev := <-rn.events:
			if pc, ok := ev.(driver.ParamChanged); ok {
				out = append(out, pc)
			}
		default:
			return out
		}
	}
}

func (rn *running) allEvents() []driver.Event {
	var out []driver.Event
	for {
		select {
		case ev := <-rn.events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func ref(kind model.StripKind, index int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: kind, Index: index}, Field: f}
}

func level(t *testing.T, db float64) model.Value {
	t.Helper()
	v, err := model.Level(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustSet(t *testing.T, sim *simwing.Sim, path string, v any) {
	t.Helper()
	if err := sim.SurfaceSet(path, v); err != nil {
		t.Fatal(err)
	}
}
