// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
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

// floodHeapBudget: the walk may hold at most this much more heap than
// before it, whatever a console sends (review H2).
const floodHeapBudget = 64 << 20

// floodConsole answers the connection with definitions made by def until
// the driver closes it, and counts the bytes it got out.
func floodConsole(c net.Conn, def func(i int) []byte, sent *atomic.Int64) {
	go func() { // drain requests so the driver's writes never block
		buf := make([]byte, 4096)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	out := wingproto.AppendSelect(nil, wingproto.ChannelData)
	for i := 0; ; i++ {
		out = wingproto.AppendEscaped(out, wingproto.AppendDefToken(nil, def(i)))
		if len(out) < 32<<10 {
			continue
		}
		if _, err := c.Write(out); err != nil {
			return
		}
		sent.Add(int64(len(out)))
		out = out[:0]
	}
}

func smallDef(i int) []byte {
	b, _ := wingproto.AppendDefBody(nil, wingproto.Def{Hash: uint32(i + 1), Index: uint16(i), Name: "n" + strconv.Itoa(i), Type: wingproto.TypeNode})
	return b
}

func enumDef(i int) []byte {
	items := make([]wingproto.EnumItem, 2000)
	for k := range items {
		items[k] = wingproto.EnumItem{Name: "ITEM" + strconv.Itoa(k), Long: "a long item name " + strconv.Itoa(k)}
	}
	b, _ := wingproto.AppendDefBody(nil, wingproto.Def{Hash: uint32(i + 1), Name: "e" + strconv.Itoa(i), Type: wingproto.TypeStringEnum, Items: items})
	return b
}

// TestConnect_DefinitionFloodIsBounded: a console that floods definitions
// (many small ones, or ones with huge enums) fails the connect with heap
// growth below floodHeapBudget.
func TestConnect_DefinitionFloodIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  func(int) []byte
	}{{"small definitions", smallDef}, {"huge enums", enumDef}} {
		t.Run(tc.name, func(t *testing.T) {
			var sent atomic.Int64
			var wg sync.WaitGroup
			dial := func(context.Context, string, string) (net.Conn, error) {
				client, server := net.Pipe()
				wg.Go(func() { floodConsole(server, tc.def, &sent); _ = server.Close() })
				return client, nil
			}
			peak, stop := sampleHeap()
			drv := New(Config{Timing: Timing{ConnectTimeout: 20 * time.Second}})
			_, err := drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP},
				driver.Options{Dial: dial, ListenPacket: memnet.New().Host(clientIP)})
			growth := stop()
			wg.Wait()
			if err == nil || !errors.Is(err, errSchema) {
				t.Fatalf("connect: %v", err)
			}
			if growth > floodHeapBudget {
				t.Fatalf("heap grew by %d MiB (budget %d MiB) after %d MiB sent", growth>>20, floodHeapBudget>>20, sent.Load()>>20)
			}
			_ = peak
		})
	}
}

// sampleHeap samples HeapAlloc every millisecond; stop returns the peak
// growth over the baseline.
func sampleHeap() (*atomic.Uint64, func() uint64) {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	var peak atomic.Uint64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > base && m.HeapAlloc-base > peak.Load() {
					peak.Store(m.HeapAlloc - base)
				}
			}
		}
	})
	return &peak, func() uint64 { close(done); wg.Wait(); return peak.Load() }
}
