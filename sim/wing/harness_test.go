// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// clientIP is the address the simulator sees for pipe clients (meters go there).
var clientIP = net.IPv4(10, 0, 0, 2)

// addrConn gives a pipe end a TCP remote address.
type addrConn struct{ net.Conn }

func (addrConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: clientIP, Port: 50000} }

// rig serves one simulator; clients connect over in-memory pipes.
type rig struct {
	t   *testing.T
	sim *Sim
	ctx context.Context
	wg  sync.WaitGroup
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, sim: New(o), ctx: ctx}
	t.Cleanup(func() { cancel(); r.sim.DropConnections(); r.wg.Wait() })
	return r
}

// pipe is a raw test client: it speaks bytes, never the driver's codec.
type pipe struct {
	t   *testing.T
	c   net.Conn
	mux rxMux
}

func (r *rig) dial() *pipe {
	r.t.Helper()
	client, server := net.Pipe()
	r.wg.Go(func() { r.sim.ServeConn(r.ctx, addrConn{server}) })
	r.t.Cleanup(func() { _ = client.Close() })
	synctest.Wait() // the simulator has admitted (or refused) the connection
	return &pipe{t: r.t, c: client, mux: rxMux{ch: -1}}
}

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// send writes raw bytes given in hex.
func (p *pipe) send(hexBytes string) {
	p.t.Helper()
	if _, err := p.c.Write(mustHex(p.t, hexBytes)); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

// recv returns the data-channel bytes that arrive within d (demuxed).
func (p *pipe) recv(d time.Duration) []byte {
	p.t.Helper()
	var data, meter []byte
	buf := make([]byte, 64<<10)
	deadline := time.Now().Add(d)
	for {
		_ = p.c.SetReadDeadline(deadline)
		n, err := p.c.Read(buf)
		p.mux.split(buf[:n], &data, &meter)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return data
		}
		if err != nil {
			return data
		}
	}
}

// closed reports whether the simulator closed the connection within d.
func (p *pipe) closed(d time.Duration) bool {
	_ = p.c.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 1024)
	for {
		_, err := p.c.Read(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return false
		}
		if err != nil {
			return true
		}
	}
}

// pushes decodes every D7 <hash> <value> in data into hash -> value tokens.
func pushes(t *testing.T, data []byte) map[uint32]token {
	t.Helper()
	out := map[uint32]token{}
	var hash uint32
	has := false
	for len(data) > 0 {
		tok, n := nextToken(data)
		if n == 0 {
			t.Fatalf("truncated reply % X", data)
		}
		if tok.kind == tkDefResp { // skip a whole definition
			n = 3 + int(binary.BigEndian.Uint16(data[1:3]))
		}
		data = data[n:]
		switch {
		case tok.kind == tkHash:
			hash, has = tok.h, true
		case has && (tok.kind == tkInt || tok.kind == tkFloat || tok.kind == tkText):
			out[hash] = tok
			has = false
		}
	}
	return out
}

// defNames returns the names of the definitions in data, in order, and
// whether a DE ended them.
func defNames(t *testing.T, data []byte) ([]string, map[string]uint32, bool) {
	t.Helper()
	var names []string
	hashes := map[string]uint32{}
	for len(data) > 0 {
		if data[0] == 0xDE {
			return names, hashes, true
		}
		if data[0] != 0xDF || len(data) < 3 {
			t.Fatalf("not a definition: % X", data[:min(8, len(data))])
		}
		n := int(binary.BigEndian.Uint16(data[1:3]))
		body := data[3 : 3+n]
		hash := binary.BigEndian.Uint32(body[4:8])
		name := string(body[11 : 11+int(body[10])])
		names = append(names, name)
		hashes[name] = hash
		data = data[3+n:]
	}
	return names, hashes, false
}

func floatBits(f float32) string {
	return hex.EncodeToString(binary.BigEndian.AppendUint32(nil, math.Float32bits(f)))
}

func equalBytes(a, b []byte) bool { return bytes.Equal(a, b) }
