// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package mobile

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	simx32 "github.com/mixproto/mixcore/sim/x32"
)

// Core-api v0.4 through the gomobile binding: a monitor seat's write
// outside its mix fails with the bare code ERR_ROLE (ErrRole) and reaches
// nothing; its own send is written; the snapshot carries buses and sends.
func TestBinding_MonitorRoleAndSends(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(simx32.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	w := &stateWaiter{}
	SetStateListener(w)
	cfg := fmt.Sprintf(`{"version":0,"family":"x32","host":"127.0.0.1","port":%d,"role":{"kind":"monitor","buses":[{"bus":2}]}}`,
		pc.LocalAddr().(*net.UDPAddr).Port)
	if _, err := Start(cfg); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = Stop(); SetStateListener(nil); cancel(); wg.Wait(); _ = pc.Close() }()
	w.waitLive(t)
	w.mu.Lock()
	snap := w.last
	w.mu.Unlock()
	if !strings.Contains(snap, `"sendFields":{"on":true,"pan":"pair","tap":"pair"}`) || !strings.Contains(snap, `"buses":[`) {
		t.Fatalf("snapshot without buses or sends: %.300s", snap)
	}
	other := `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"sendLevel","bus":1},"value":0,"origin":{"kind":"gesture","clientId":"m","seq":1}}`
	if err := Apply(other); err == nil || err.Error() != ErrRole {
		t.Fatalf("send to another mix = %v, want %s", err, ErrRole)
	}
	own := strings.Replace(strings.Replace(other, `"bus":1`, `"bus":2`, 1), `"value":0`, `"value":-10`, 1)
	if err := Apply(own); err != nil {
		t.Fatalf("own send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for sim.WriteCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ws := sim.Writes(); len(ws) != 1 || ws[0].Address != "/ch/01/mix/02/level" {
		t.Fatalf("writes %+v, want only the musician's own send", ws)
	}
}
