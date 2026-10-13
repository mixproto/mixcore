// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package wing

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// loopback serves sim/wing on real 127.0.0.1 TCP and UDP sockets.
func loopback(t *testing.T, o simwing.Options) (*simwing.Sim, driver.Endpoint) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simwing.New(o)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, ln) })
	wg.Go(func() { _ = sim.ServeUDP(ctx, pc) })
	t.Cleanup(func() { cancel(); sim.DropConnections(); wg.Wait(); _ = pc.Close() })
	return sim, driver.Endpoint{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
}

// TestLoopback_ConnectWriteAndClientLimit: real sockets; the 17th TCP
// connection is refused (WG-F22) and the driver reports a connect error.
func TestLoopback_ConnectWriteAndClientLimit(t *testing.T) {
	sim, ep := loopback(t, simwing.Options{Firmware: "3.0.6-0-g0:release"})
	drv := New(Config{})
	sess, err := drv.Connect(context.Background(), ep, driver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx, make(chan driver.Event, 1024)) }()
	if err := sess.Apply(driver.Command{Param: ref(model.StripInput, 40, model.FieldMute), Value: model.Bool(true), Origin: gesture}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for v, _ := sim.Value("/ch/40/mute"); v != 1 && time.Now().Before(deadline); v, _ = sim.Value("/ch/40/mute") {
		time.Sleep(10 * time.Millisecond)
	}
	if v, _ := sim.Value("/ch/40/mute"); v != 1 {
		t.Fatalf("mute not applied over TCP: %v", v)
	}
	var others []net.Conn
	for range 15 { // 16 slots: ours plus 15
		c, err := net.Dial("tcp4", net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)))
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, c)
	}
	time.Sleep(100 * time.Millisecond)
	cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ccancel()
	if s2, err := drv.Connect(cctx, ep, driver.Options{}); err == nil {
		_ = s2.Close()
		t.Fatal("a 17th connection was served")
	}
	for _, c := range others {
		_ = c.Close()
	}
	cancel()
	<-done
	_ = sess.Close()
}
