// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xair

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
	simxair "github.com/mixproto/mixcore/sim/xair"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	mixerIP  = "10.0.0.1"
	clientIP = "10.0.0.2"
)

var gesture = driver.Origin{Kind: driver.OriginGesture, ClientID: "ui", Seq: 1}

// rig is a simulator and a driver on one in-memory network.
type rig struct {
	t     *testing.T
	net   *memnet.Network
	sim   *simxair.Sim
	drv   *Driver
	slot  *meters.Slot
	mu    sync.Mutex
	audit []driver.WriteRecord
}

func newRig(t *testing.T, simOpts simxair.Options) *rig {
	t.Helper()
	n := memnet.New()
	r := &rig{t: t, net: n, sim: simxair.New(simOpts), drv: New(Config{}), slot: &meters.Slot{}}
	pc, err := n.ListenPacket(mixerIP + ":10024") // XA-F01
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = r.sim.Serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	return r
}

func (r *rig) opts() driver.Options {
	return driver.Options{
		ListenPacket: r.net.Host(clientIP),
		Meters:       r.slot,
		WriteAudit: func(w driver.WriteRecord) {
			r.mu.Lock()
			r.audit = append(r.audit, w)
			r.mu.Unlock()
		},
	}
}

func (r *rig) connect() driver.Session {
	r.t.Helper()
	sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: mixerIP}, r.opts())
	if err != nil {
		r.t.Fatalf("Connect: %v", err)
	}
	r.t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// run starts Run and returns the event channel and a stop function that
// returns Run's error.
func (r *rig) run(sess driver.Session) (<-chan driver.Event, func() error) {
	events := make(chan driver.Event, 256)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx, events) }()
	var once sync.Once
	var err error
	stop := func() error { once.Do(func() { cancel(); err = <-done }); return err }
	r.t.Cleanup(func() { _ = stop() })
	return events, stop
}

// assertWritesAudited: every write the mixer received matches one audited
// user write, in order (go-core-standards section 11).
func (r *rig) assertWritesAudited() {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	writes := r.sim.Writes()
	if len(writes) != len(r.audit) {
		r.t.Fatalf("mixer received %d writes, driver audited %d", len(writes), len(r.audit))
	}
	for i, w := range writes {
		if a := r.audit[i]; w.Address != a.Address || !a.Origin.MayWrite() {
			r.t.Fatalf("write %d: mixer got %s, audit has %s with origin %v", i, w.Address, a.Address, a.Origin)
		}
	}
}

// subscriber is a raw /xremote client (another app) on its own port.
func (r *rig) subscriber(ip string) net.PacketConn {
	r.t.Helper()
	pc, err := r.net.ListenPacket(ip + ":0")
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = pc.Close() })
	return pc
}

func sendTo(t *testing.T, pc net.PacketConn, addr string, args ...osc.Arg) {
	t.Helper()
	b, err := osc.AppendString(nil, addr, args...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.WriteTo(b, net.UDPAddrFromAddrPort(netip.MustParseAddrPort(mixerIP+":10024"))); err != nil {
		t.Fatal(err)
	}
}

func ref(i int, f model.Field) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: i}, Field: f}
}

func grp(kind model.StripKind, i int) model.ParamRef {
	return model.ParamRef{Strip: model.StripRef{Kind: kind, Index: i}, Field: model.FieldMute}
}

func mainRef(f model.Field) model.ParamRef { return model.ParamRef{Strip: model.MainRef, Field: f} }

func level(t *testing.T, db float64) model.Value {
	t.Helper()
	v, err := model.Level(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// nextOf returns the next event of type T within wait, skipping others.
func nextOf[T driver.Event](events <-chan driver.Event, wait time.Duration) (T, bool) {
	deadline := time.After(wait)
	for {
		select {
		case ev := <-events:
			if e, ok := ev.(T); ok {
				return e, true
			}
		case <-deadline:
			var zero T
			return zero, false
		}
	}
}

func drain(events <-chan driver.Event) []driver.Event {
	var out []driver.Event
	for {
		select {
		case ev := <-events:
			out = append(out, ev)
		default:
			return out
		}
	}
}
