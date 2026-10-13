// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"bytes"
	"context"
	"errors"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/memnet"
	"github.com/mixproto/mixcore/internal/wingproto"
)

// Hashes of the probe's root nodes (no 0xDF byte, so no escaping).
var probeRoots = []struct {
	name string
	hash uint32
}{{"ch", 0x70B10139}, {"main", 0x11000002}, {"dca", 0x11000003}, {"mgrp", 0x11000004}, {"io", 0x11000005}, {"cfg", 0x11000006}, {"$syscfg", 0x11000007}}

// enumProbeConsole is the security reviewer's probe: the root's 7 nodes,
// then, for the /ch and /main definition requests, perNode enum
// definitions of items items each (one-byte names: the most decoded memory
// per wire byte), then a DE per request.
func enumProbeConsole(c net.Conn, perNode, items int) {
	var got []byte
	buf := make([]byte, 4096)
	rootSent, floodSent := false, false
	out := func(b []byte) bool {
		_, err := c.Write(wingproto.AppendFrame(nil, wingproto.ChannelData, b))
		return err == nil
	}
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		got = append(got, buf[:n]...)
		if !rootSent && bytes.Contains(got, []byte{0xDA, 0xDD}) {
			rootSent = true
			var b []byte
			for i, r := range probeRoots {
				body, _ := wingproto.AppendDefBody(nil, wingproto.Def{Hash: r.hash, Index: uint16(i + 1), Name: r.name, Type: wingproto.TypeNode})
				b = wingproto.AppendDefToken(b, body)
			}
			if !out(append(b, 0xDE)) {
				return
			}
		}
		if rootSent && !floodSent && bytes.Contains(got, wingproto.AppendDefsOf(nil, probeRoots[1].hash)) {
			floodSent = true
			if !floodEnums(out, perNode, items) {
				return
			}
		}
	}
}

func floodEnums(out func([]byte) bool, perNode, items int) bool {
	its := make([]wingproto.EnumItem, items)
	for k := range its {
		its[k] = wingproto.EnumItem{Name: "I"}
	}
	for _, parent := range []uint32{probeRoots[0].hash, probeRoots[1].hash} {
		var b []byte
		for i := range perNode {
			body, _ := wingproto.AppendDefBody(nil, wingproto.Def{Parent: parent, Hash: parent + uint32(i) + 1, Name: "e" + strconv.Itoa(i),
				Type: wingproto.TypeStringEnum, Items: its})
			b = wingproto.AppendDefToken(b, body)
			if len(b) > 64<<10 {
				if !out(b) {
					return false
				}
				b = b[:0]
			}
		}
		if !out(b) {
			return false
		}
	}
	return out([]byte{0xDE, 0xDE, 0xDE, 0xDE, 0xDE, 0xDE, 0xDE})
}

// TestConnect_EnumProbeHeapBounded (security re-check 3): the walk budget
// is charged with decoded size, so enum-heavy definitions keep the heap in
// use below 64 MB through Driver.Connect.
func TestConnect_EnumProbeHeapBounded(t *testing.T) {
	for _, items := range []int{256, 1024} {
		t.Run(strconv.Itoa(items)+" items", func(t *testing.T) {
			var wg sync.WaitGroup
			dial := func(context.Context, string, string) (net.Conn, error) {
				client, server := net.Pipe()
				wg.Go(func() { enumProbeConsole(server, 2030, items); _ = server.Close() })
				return client, nil
			}
			stop := sampleHeapInuse()
			_, err := New(Config{Timing: Timing{ConnectTimeout: 30 * time.Second}}).Connect(context.Background(), driver.Endpoint{Host: consoleIP},
				driver.Options{Dial: dial, ListenPacket: memnet.New().Host(clientIP)})
			peak := stop()
			wg.Wait()
			if err == nil || !errors.Is(err, errSchema) {
				t.Fatalf("connect: %v", err)
			}
			t.Logf("peak heap in use growth %d KiB", peak>>10)
			if peak > floodHeapBudget {
				t.Fatalf("heap in use grew by %d MiB (budget %d MiB)", peak>>20, floodHeapBudget>>20)
			}
		})
	}
}

// sampleHeapInuse samples HeapInuse every millisecond; stop returns the
// peak growth over the baseline.
func sampleHeapInuse() func() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapInuse
	var peak atomic.Uint64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tk := time.NewTicker(time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapInuse > base && m.HeapInuse-base > peak.Load() {
					peak.Store(m.HeapInuse - base)
				}
			}
		}
	})
	return func() uint64 { close(done); wg.Wait(); return peak.Load() }
}

// TestDecodedSize_ChargesItems: an enum definition is charged at least
// decodedItemBytes per item, whatever its wire size.
func TestDecodedSize_ChargesItems(t *testing.T) {
	d := wingproto.Def{Name: "e", Type: wingproto.TypeStringEnum, Items: make([]wingproto.EnumItem, 200)}
	if got := decodedSize(d); got < 200*decodedItemBytes {
		t.Fatalf("decoded size %d", got)
	}
	if wingproto.MaxEnumItems != 256 || maxWalkBytes != 8<<20 {
		t.Fatalf("caps %d items, %d bytes", wingproto.MaxEnumItems, maxWalkBytes)
	}
}
