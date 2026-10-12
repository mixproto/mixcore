// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/osc"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// otherApps keeps n other /xremote clients registered (renewing every 3 s)
// until stop is called: the console's four push slots are taken (X32-F21).
func otherApps(t *testing.T, n *memnet.Network, count int) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	to := net.UDPAddrFromAddrPort(netip.MustParseAddrPort(consoleIP + ":10023"))
	pkt, _ := osc.AppendString(nil, "/xremote")
	for i := range count {
		pc, err := n.ListenPacket(fmt.Sprintf("10.0.7.%d:0", i+1))
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			defer pc.Close()
			tick := time.NewTicker(3 * time.Second)
			defer tick.Stop()
			for {
				_, _ = pc.WriteTo(pkt, to)
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		})
	}
	synctest.Wait()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); wg.Wait() }) }
	t.Cleanup(stop)
	return stop
}

func muteCmd(ch int, on bool) string {
	return fmt.Sprintf(`{"version":0,"type":"set","param":{"strip":{"kind":"input","index":%d},"field":"mute"},"value":%t,"origin":{"kind":"gesture","clientId":"ui"}}`, ch, on)
}

// SR-17: four other apps hold the console's push slots; our writes get no
// confirmation, the state says limited, input locks; when a slot frees, the
// next update clears it.
func TestLimited_StateLocksInputAndClears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnvWith(t, simx32.Options{Name: "FOH"})
		stop := otherApps(t, e.net, simx32.DefaultMaxPushClients)
		e.startLive(t)
		for i, on := range []bool{true, true, true} { // each mutes an unmuted channel: a change
			if err := e.eng.Apply(muteCmd(1+i, on)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(3 * time.Second)
		st := e.rec.last()
		if st.Status != "live" || !st.Limited || st.LimitedReason != "noEchoOfOwnWrites" {
			t.Fatalf("state = %+v, want live and limited", st)
		}
		if err := e.eng.Apply(muteCmd(4, true)); CodeOf(err) != CodeStale {
			t.Fatalf("Apply while limited = %v, want %s", err, CodeStale)
		}
		stop()
		time.Sleep(13 * time.Second) // their slots expire; our renewal takes one
		_ = e.sim.SurfaceSet("/ch/09/mix/on", osc.Int(0))
		synctest.Wait()
		if st := e.rec.last(); st.Limited || st.LimitedReason != "" {
			t.Fatalf("after an update: %+v", st)
		}
	})
}
