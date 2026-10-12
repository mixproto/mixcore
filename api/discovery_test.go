// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/memnet"
	simx32 "github.com/mixproto/mixcore/sim/x32"
)

type discoveryRecorder struct {
	mu     sync.Mutex
	msgs   []discoveryResultJSON
	onMsg  func()
	panics bool
}

func (r *discoveryRecorder) OnDiscovery(s string) {
	var m discoveryResultJSON
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	onMsg := r.onMsg
	r.mu.Unlock()
	if onMsg != nil {
		onMsg()
	}
	if r.panics {
		panic("listener bug")
	}
}

func (r *discoveryRecorder) all() []discoveryResultJSON {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]discoveryResultJSON(nil), r.msgs...)
}

// serveSim runs a simulator at ip:10023 on n until the test ends.
func serveSim(t *testing.T, n *memnet.Network, ip string, opts simx32.Options) *simx32.Sim {
	t.Helper()
	pc, err := n.ListenPacket(ip + ":10023")
	if err != nil {
		t.Fatal(err)
	}
	sim := simx32.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sim.Serve(ctx, pc) })
	t.Cleanup(func() { cancel(); wg.Wait(); _ = pc.Close() })
	return sim
}

const discoverOK = `{"version":0,"family":"x32","broadcast":true,"hosts":[{"host":"10.0.9.9"}],"waitMs":500}`

func TestDiscover_FindsBroadcastAndManualConsolesWithoutWriting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		a := serveSim(t, n, "10.0.0.1", simx32.Options{Name: "FOH", Model: "X32"})
		b := serveSim(t, n, "10.0.9.9", simx32.Options{Name: "MON", Model: "M32"})
		eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2")})
		defer eng.Close()
		rec := &discoveryRecorder{}
		eng.SetDiscoveryListener(rec)

		if err := eng.Discover(discoverOK); err != nil {
			t.Fatal(err)
		}
		if err := eng.Discover(discoverOK); CodeOf(err) != CodeBusy {
			t.Fatalf("second Discover while one runs = %v, want %s", err, CodeBusy)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		msgs := rec.all()
		if len(msgs) != 1 || msgs[0].Status != "done" || msgs[0].Type != "discovery" || len(msgs[0].Consoles) != 2 {
			t.Fatalf("results = %+v", msgs)
		}
		byName := map[string]foundJSON{}
		for _, c := range msgs[0].Consoles {
			byName[c.Name] = c
		}
		if c := byName["MON"]; c.Host != "10.0.9.9" || c.Port != 10023 || c.Model != "M32" || c.Family != "x32" {
			t.Fatalf("MON = %+v", c)
		}
		if len(a.Writes())+len(b.Writes()) != 0 || len(a.Subscribers())+len(b.Subscribers()) != 0 {
			t.Fatal("discovery must only read: no writes, no subscriptions")
		}
		if err := eng.Discover(discoverOK); err != nil {
			t.Fatalf("Discover after the previous one finished = %v", err)
		}
	})
}

func TestDiscover_ListenerMayStartTheNextProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := memnet.New()
		serveSim(t, n, "10.0.9.9", simx32.Options{})
		eng := NewEngineWith(Options{ListenPacket: n.Host("10.0.0.2")})
		defer eng.Close()
		rec := &discoveryRecorder{}
		var again error
		var once sync.Once
		rec.onMsg = func() { once.Do(func() { again = eng.Discover(discoverOK) }) }
		eng.SetDiscoveryListener(rec)
		if err := eng.Discover(discoverOK); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if again != nil || len(rec.all()) != 2 {
			t.Fatalf("re-scan from the listener: err %v, %d results", again, len(rec.all()))
		}
	})
}

func TestDiscover_NetworkFailureIsReportedAsCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		refuse := func(context.Context, string, string) (net.PacketConn, error) {
			return nil, errors.New("local network access denied")
		}
		eng := NewEngineWith(Options{ListenPacket: refuse})
		defer eng.Close()
		rec := &discoveryRecorder{panics: true} // a panicking listener is contained
		eng.SetDiscoveryListener(rec)
		if err := eng.Discover(discoverOK); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		msgs := rec.all()
		if len(msgs) != 1 || msgs[0].Status != "failed" || msgs[0].Error != CodeNetwork || msgs[0].Consoles == nil {
			t.Fatalf("results = %+v", msgs)
		}
		eng.SetDiscoveryListener(nil)
		if err := eng.Discover(discoverOK); err != nil {
			t.Fatalf("Discover without a listener = %v", err)
		}
		synctest.Wait()
	})
}

func TestDiscover_RejectsBadRequests(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()
	bad := []struct{ name, req string }{
		{"not json", `{`},
		{"no version", `{"family":"x32","broadcast":true}`},
		{"wrong version", `{"version":1,"family":"x32","broadcast":true}`},
		{"unknown family", `{"version":0,"family":"wing","broadcast":true}`},
		{"nothing to probe", `{"version":0,"family":"x32"}`},
		{"hostname", `{"version":0,"family":"x32","hosts":[{"host":"x32.local"}]}`},
		{"port out of range", `{"version":0,"family":"x32","hosts":[{"host":"10.0.0.1","port":70000}]}`},
		{"wait too short", `{"version":0,"family":"x32","broadcast":true,"waitMs":5}`},
		{"wait too long", `{"version":0,"family":"x32","broadcast":true,"waitMs":60000}`},
		{"unknown field", `{"version":0,"family":"x32","broadcast":true,"timeout":3}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := eng.Discover(tc.req); CodeOf(err) != CodeInvalidConfig {
				t.Fatalf("Discover(%s) = %v, want %s", tc.req, err, CodeInvalidConfig)
			}
		})
	}
	tooMany := `{"version":0,"family":"x32","hosts":[` + repeatHosts(maxDiscoverHosts+1) + `]}`
	if err := eng.Discover(tooMany); CodeOf(err) != CodeInvalidConfig {
		t.Fatalf("too many hosts = %v", err)
	}
}

func repeatHosts(n int) string {
	s := ""
	for i := range n {
		if i > 0 {
			s += ","
		}
		s += `{"host":"10.0.0.1"}`
	}
	return s
}

func TestDiscoveryJSON_Golden(t *testing.T) {
	got := encodeDiscovery(familyX32, []x32.Found{{
		Endpoint: driver.Endpoint{Host: "192.168.1.62", Port: 10023},
		Identity: driver.Identity{Name: "X32-02-4A-53", Model: "X32", Firmware: "3.04"},
	}}, nil)
	want := `{"version":0,"type":"discovery","status":"done","consoles":[{"family":"x32","host":"192.168.1.62","port":10023,"name":"X32-02-4A-53","model":"X32","firmware":"3.04"}]}`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	if got := encodeDiscovery(familyX32, nil, errors.New("x")); got != `{"version":0,"type":"discovery","status":"failed","error":"ERR_NETWORK","consoles":[]}` {
		t.Fatalf("failed result %s", got)
	}
}

func FuzzParseDiscover(f *testing.F) {
	f.Add(discoverOK)
	f.Add(`{"version":0,"family":"x32","broadcast":true}`)
	f.Add(`{"version":0,"family":"x32","hosts":[{"host":"::1","port":1}],"waitMs":100}`)
	f.Fuzz(func(t *testing.T, in string) {
		req, err := parseDiscover(in)
		if err != nil {
			if CodeOf(err) != CodeInvalidConfig {
				t.Fatalf("parse error with code %s", CodeOf(err))
			}
			return
		}
		if !req.broadcast && len(req.manual) == 0 {
			t.Fatal("accepted a request with nothing to probe")
		}
		if req.wait < minDiscoverWaitMs*time.Millisecond || req.wait > maxDiscoverWaitMs*time.Millisecond || len(req.manual) > maxDiscoverHosts {
			t.Fatalf("accepted out-of-range request %+v", req)
		}
		for _, ap := range req.manual {
			if !ap.IsValid() || ap.Port() == 0 {
				t.Fatalf("accepted target %v", ap)
			}
		}
	})
}
