// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestLink_HostileSessionCannotRouteLaterWrites (security re-check 1): a
// first session against a console that swaps the hashes of /ch/2/fdr and
// /main/1/fdr (the channel list unchanged) leaves nothing a later session
// on the same link trusts: its ch 2 write reaches /ch/2/fdr, never the main.
func TestLink_HostileSessionCannotRouteLaterWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		honest := simwing.New(simwing.Options{})
		ch2, _ := honest.Hash("/ch/2/fdr")
		main1, _ := honest.Hash("/main/1/fdr")
		hostile := simwing.New(simwing.Options{HashOverrides: map[string]uint32{"/ch/2/fdr": main1, "/main/1/fdr": ch2}})
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		defer func() { cancel(); hostile.DropConnections(); honest.DropConnections(); wg.Wait() }()
		dials := 0
		r := newRig(t, simwing.Options{})
		opts := r.opts()
		opts.Dial = func(context.Context, string, string) (net.Conn, error) {
			sim := honest
			if dials++; dials == 1 {
				sim = hostile
			}
			client, server := net.Pipe()
			console := &net.TCPAddr{IP: net.ParseIP(consoleIP), Port: DefaultPort}
			wg.Go(func() {
				sim.ServeConn(ctx, pipeEnd{Conn: server, local: console, remote: &net.TCPAddr{IP: net.ParseIP(clientIP), Port: 1}})
			})
			return pipeEnd{Conn: client, remote: console}, nil
		}
		l, err := r.drv.OpenLink(ctx, driver.Endpoint{Host: consoleIP}, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		for range 2 {
			s, err := l.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if dials == 1 {
				_ = s.Close()
				continue
			}
			rn := r.run(s.(*session))
			if err := rn.s.Apply(driver.Command{Param: ref(model.StripInput, 2, model.FieldFader), Value: level(t, -10), Origin: gesture}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Second)
			synctest.Wait()
		}
		if v, _ := honest.Value("/main/1/fdr"); v != -144.0 {
			t.Fatalf("the ch 2 gesture moved the main fader to %v", v)
		}
		if v, _ := honest.Value("/ch/2/fdr"); v != -10.0 {
			t.Fatalf("ch 2 fader %v", v)
		}
	})
}

// TestWalk_DocumentedHashMismatchRefused (security re-check 1): a leaf
// whose hash differs from the one the protocol document lists (WG-F32)
// fails the walk.
func TestWalk_DocumentedHashMismatchRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{HashOverrides: map[string]uint32{"/ch/1/mute": 0x11223344}})
		_, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, r.opts())
		if !errors.Is(err, errSchema) || !errors.Is(err, errDocumentedHash) {
			t.Fatalf("connect: %v", err)
		}
	})
}
