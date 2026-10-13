// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

const (
	readBuf = 4096
	// maxBuffered caps unparsed bytes of a client; more drops the client.
	maxBuffered = 64 << 10
)

// client is one TCP connection. Its reader goroutine parses and answers
// under Sim.mu; a writer goroutine sends queued bytes, so a slow reader on
// the other side never blocks the simulator.
type client struct {
	sim    *Sim
	c      net.Conn
	id     int
	remote netip.Addr
	out    chan []byte
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup

	// Reader-goroutine state (used under Sim.mu).
	mux   rxMux
	data  []byte
	meter []byte
	sel   *node
	reply []byte
	asked int      // requests of the current burst (MaxPipelined)
	sub   meterSub // guarded by Sim.mu (the meter goroutine reads it)
}

func newClient(s *Sim, c net.Conn, id int) *client {
	cl := &client{sim: s, c: c, id: id, out: make(chan []byte, outQueue), done: make(chan struct{}), mux: rxMux{ch: -1}}
	switch a := c.RemoteAddr().(type) {
	case *net.TCPAddr:
		cl.remote = a.AddrPort().Addr().Unmap()
	case *net.UDPAddr:
		cl.remote = a.AddrPort().Addr().Unmap()
	}
	return cl
}

// drop closes the connection with a FIN. Idempotent.
func (cl *client) drop() {
	cl.once.Do(func() {
		close(cl.done)
		_ = cl.c.Close()
	})
}

// send queues bytes; a client whose queue is full is dropped.
func (cl *client) send(b []byte) {
	select {
	case cl.out <- b:
	case <-cl.done:
	default:
		cl.drop()
	}
}

// frame wraps a data-channel payload: DF D1 and every DF escaped (WG-F13).
func frame(payload []byte) []byte {
	return escapeOut([]byte{esc, chanFirst + chanData}, payload)
}

func (cl *client) writer() {
	for {
		select {
		case b := <-cl.out:
			if _, err := cl.c.Write(b); err != nil {
				cl.drop()
				return
			}
		case <-cl.done:
			return
		}
	}
}

// run reads until the connection ends. No byte for IdleTimeout closes it
// (WG-F25), except while the simulator is paused.
func (cl *client) run() {
	cl.wg.Go(cl.writer)
	defer func() { cl.drop(); cl.wg.Wait() }()
	buf := make([]byte, readBuf)
	for {
		_ = cl.c.SetReadDeadline(time.Now().Add(cl.sim.opts.IdleTimeout))
		n, err := cl.c.Read(buf)
		if n > 0 && !cl.sim.handleBytes(cl, buf[:n]) {
			return
		}
		if errors.Is(err, os.ErrDeadlineExceeded) && cl.sim.isPaused() {
			continue
		}
		if err != nil {
			return
		}
	}
}

func (s *Sim) isPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

// handleBytes processes received bytes; false drops the client.
func (s *Sim) handleBytes(cl *client, b []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		return true // a stalled console reads nothing and answers nothing
	}
	cl.mux.split(b, &cl.data, &cl.meter)
	if len(cl.data) > maxBuffered || len(cl.meter) > maxBuffered {
		return false
	}
	cl.reply, cl.asked = cl.reply[:0], 0
	cl.data = cl.data[s.processData(cl, cl.data):]
	cl.meter = cl.meter[s.processMeter(cl, cl.meter):]
	if len(cl.reply) > 0 {
		cl.send(frame(cl.reply))
	}
	return true
}

// processData runs every complete data-channel token and returns how many
// bytes were used (WG-F30, WG-F31).
func (s *Sim) processData(cl *client, b []byte) int {
	used := 0
	for {
		tok, n := nextToken(b[used:])
		if n == 0 {
			return used
		}
		used += n
		s.token(cl, tok)
	}
}

func (s *Sim) token(cl *client, tok token) {
	switch tok.kind {
	case tkHash:
		cl.sel = s.tree.byHash[tok.h]
	case tkRoot:
		cl.sel = s.tree.root
	case tkUp:
		if cl.sel != nil && cl.sel.parent != nil {
			cl.sel = cl.sel.parent
		}
	case tkName:
		cl.sel = s.childOf(cl.sel, func(n *node) *node { return n.child(string(tok.b)) })
	case tkIndex:
		cl.sel = s.childOf(cl.sel, func(n *node) *node {
			if int(tok.i) <= len(n.children) {
				return n.children[tok.i-1]
			}
			return nil
		})
	case tkData, tkDefs:
		if cl.asked++; s.opts.MaxPipelined > 0 && cl.asked > s.opts.MaxPipelined {
			return // over the console's pipeline: dropped (item 3)
		}
		if tok.kind == tkData {
			s.dataRequest(cl)
		} else {
			s.defRequest(cl)
		}
	case tkInt, tkFloat, tkRaw, tkText, tkClick, tkStep:
		s.write(cl, cl.sel, tok)
	}
}

func (s *Sim) childOf(n *node, pick func(*node) *node) *node {
	if n == nil {
		n = s.tree.root
	}
	return pick(n)
}

// dataRequest answers DC: the value of a leaf, or every leaf under a node
// (WG-F31), then DE if DataEndsWithDE (item 3).
func (s *Sim) dataRequest(cl *client) {
	n := cl.sel
	s.logEntry(cl, ClassRead, pathOf(n), "", false)
	if n != nil {
		for _, l := range leaves(n, nil) {
			cl.reply = appendPush(cl.reply, l.hash, l.val)
		}
	}
	if s.opts.DataEndsWithDE {
		cl.reply = append(cl.reply, 0xDE)
	}
}

// defRequest answers DD with the definitions of the selected node's
// children (WG-F33), then DE (WG-F30).
func (s *Sim) defRequest(cl *client) {
	n := cl.sel
	s.logEntry(cl, ClassDefs, pathOf(n), "", false)
	if n != nil {
		for _, c := range n.children {
			cl.reply = c.appendDef(cl.reply)
		}
	}
	cl.reply = append(cl.reply, 0xDE)
}

func pathOf(n *node) string {
	if n == nil {
		return "?"
	}
	if n.path == "" {
		return "/"
	}
	return n.path
}

// pushAll sends D7 <hash> <value> for nodes to every client except skip.
func (s *Sim) pushAll(nodes []*node, skip *client) {
	if len(nodes) == 0 {
		return
	}
	var payload []byte
	for _, n := range nodes {
		payload = appendPush(payload, n.hash, n.val)
	}
	for cl := range s.conns {
		if cl != skip {
			cl.send(frame(payload))
		}
	}
}
