// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/internal/wingproto"
)

const (
	// inboundCap bounds messages waiting for the owner goroutine. A full
	// queue blocks the reader; TCP flow control then slows the console.
	inboundCap = 256
	// readBufSize is one TCP read.
	readBufSize = 16 << 10
	// maxPending caps unparsed data-channel bytes: one maximal definition
	// token plus a read buffer. More means the stream cannot be parsed.
	maxPending = wingproto.MaxDefLen + 16 + readBufSize
)

// errStream: the console's byte stream cannot be parsed any more.
var errStream = errors.New("wing: unparseable stream from the console")

// msgKind is what the reader assembled from the token stream.
type msgKind uint8

const (
	msgValue msgKind = iota + 1 // D7 <hash> <value>: a reply or a push (WG-F31)
	msgDef                      // DF ...: one node definition (WG-F33)
	msgEnd                      // DE: the end of a request's answer (WG-F31)
)

// inMsg is one message from the console.
type inMsg struct {
	kind msgKind
	hash uint32
	val  wireValue
	def  wingproto.Def
	bad  bool // msgDef: the definition did not parse (counted, never used)
	size int  // msgDef: body bytes, for the walk's byte limit
	at   time.Time
}

// conn is one TCP connection to the console (WG-F10, WG-F15) and its reader.
// Writes happen on the owner goroutine only; the reader only parses.
type conn struct {
	tcp     net.Conn
	inbound chan inMsg
	errs    chan error
	done    chan struct{}
	base    time.Time
	lastRx  atomic.Int64 // nanoseconds after base of the last complete message
	badDefs atomic.Int64 // definitions that did not parse (logged by the session)
	// walking is set by the session while a schema walk runs; outside it
	// definition tokens are skipped without parsing (review H2).
	walking atomic.Bool
	wg      sync.WaitGroup
	once    sync.Once
	writeTO time.Duration
	tx      []byte
}

// dialConn opens the TCP connection and selects the data channel (WG-F15:
// DF D1 is the whole session start; there is no login).
func dialConn(ctx context.Context, dial func(ctx context.Context, network, address string) (net.Conn, error),
	addr string, writeTimeout time.Duration) (*conn, error) {
	tcp, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("wing: dial %s: %w", addr, err)
	}
	c := &conn{tcp: tcp, inbound: make(chan inMsg, inboundCap), errs: make(chan error, 1), done: make(chan struct{}),
		base: time.Now(), writeTO: writeTimeout}
	c.lastRx.Store(0)
	if err := c.write(wingproto.AppendSelect(nil, wingproto.ChannelData)); err != nil {
		_ = tcp.Close()
		return nil, err
	}
	safe.Go(&c.wg, c.fail, func() { c.fail(c.readLoop()) })
	return c, nil
}

// fail reports a reader failure once; a normal close is not a failure.
func (c *conn) fail(err error) {
	if err == nil {
		return
	}
	select {
	case <-c.done:
	case c.errs <- err:
	default:
	}
}

// close sends FIN (a slot of the console's 16 or 24 is freed at once,
// WG-F22) and joins the reader. Idempotent.
func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.tcp.Close()
		c.wg.Wait()
	})
}

// lastRxAt is when the last complete message arrived; the connect time
// before the first one.
func (c *conn) lastRxAt() time.Time { return c.base.Add(time.Duration(c.lastRx.Load())) }

// write sends raw bytes with a deadline, so a stalled console fails the
// session instead of blocking the owner goroutine.
func (c *conn) write(b []byte) error {
	if err := c.tcp.SetWriteDeadline(time.Now().Add(c.writeTO)); err != nil {
		return fmt.Errorf("wing: write deadline: %w", err)
	}
	if _, err := c.tcp.Write(b); err != nil {
		return fmt.Errorf("wing: write: %w", err)
	}
	return nil
}

// writeData sends data-channel tokens; the channel stays selected, so every
// 0xDF of the payload is escaped and nothing else is added (WG-F13).
func (c *conn) writeData(payload []byte) error {
	c.tx = wingproto.AppendEscaped(c.tx[:0], payload)
	return c.write(c.tx)
}

// writeMeters sends meter-channel tokens, then selects the data channel
// again: DF D3 ... DF D1 (WG-F50, the vendor sequence of WG-F51).
func (c *conn) writeMeters(payload []byte) error {
	c.tx = wingproto.AppendSelect(wingproto.AppendFrame(c.tx[:0], wingproto.ChannelMeters, payload), wingproto.ChannelData)
	return c.write(c.tx)
}

// readLoop reads the stream until the connection closes. It returns nil
// after close, io.EOF wrapped when the console closed, or the read error.
func (c *conn) readLoop() error {
	buf := make([]byte, readBufSize)
	var (
		mux  wingproto.Demux
		data []byte
		asm  = assembler{skipDefs: func() bool { return !c.walking.Load() }}
	)
	for {
		n, err := c.tcp.Read(buf)
		if n > 0 {
			data = mux.Append(data, buf[:n], wingproto.ChannelData)
			rest, perr := asm.feed(data, time.Now(), c.deliver)
			if perr != nil {
				return perr
			}
			data = append(data[:0], rest...)
			if len(data) > maxPending {
				return fmt.Errorf("%w: %d bytes without a complete token", errStream, len(data))
			}
		}
		if err != nil {
			select {
			case <-c.done:
				return nil
			default:
			}
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("wing: console closed the connection: %w", err)
			}
			return fmt.Errorf("wing: read: %w", err)
		}
	}
}

// deliver hands one message to the owner; false once the connection closed.
func (c *conn) deliver(m inMsg) bool {
	c.lastRx.Store(int64(time.Since(c.base))) // a complete message is a sign of life (review go L6)
	if m.bad {
		c.badDefs.Add(1) // still delivered: its bytes count against the walk's limit
	}
	select {
	case c.inbound <- m:
		return true
	case <-c.done:
		return false
	}
}

// assembler turns tokens into messages: a hash followed by a value is one
// value message; a definition or an end token is one message each.
type assembler struct {
	hash    uint32
	hasHash bool
	// skipDefs, when set and true, drops definition tokens unparsed.
	skipDefs func() bool
}

// feed parses every complete token of data and returns the unparsed rest
// (which aliases data). It never panics on malformed input.
func (a *assembler) feed(data []byte, at time.Time, deliver func(inMsg) bool) ([]byte, error) {
	for len(data) > 0 {
		tok, n, err := wingproto.Parse(data)
		if errors.Is(err, wingproto.ErrShort) {
			return data, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errStream, err)
		}
		data = data[n:]
		if m, ok := a.message(tok, at); ok && !deliver(m) {
			return nil, nil
		}
	}
	return data, nil
}

func (a *assembler) message(tok wingproto.Token, at time.Time) (inMsg, bool) {
	switch tok.Kind {
	case wingproto.KindHash:
		a.hash, a.hasHash = tok.Hash, true
		return inMsg{}, false
	case wingproto.KindDef:
		a.hasHash = false
		if a.skipDefs != nil && a.skipDefs() {
			return inMsg{}, false
		}
		d, err := wingproto.ParseDef(tok.Bytes)
		if err != nil {
			return inMsg{kind: msgDef, bad: true, size: len(tok.Bytes), at: at}, true
		}
		return inMsg{kind: msgDef, def: d, size: len(tok.Bytes), at: at}, true
	case wingproto.KindEnd:
		a.hasHash = false
		return inMsg{kind: msgEnd, at: at}, true
	}
	v, isValue := valueOf(tok)
	if !isValue || !a.hasHash {
		a.hasHash = false // navigation or an orphan value: nothing we asked for
		return inMsg{}, false
	}
	a.hasHash = false
	return inMsg{kind: msgValue, hash: a.hash, val: v, at: at}, true
}
