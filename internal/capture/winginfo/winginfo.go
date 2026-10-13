// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package winginfo asks a WING console for its own node definitions and
// values, read-only: a schema walk for the hardware session of
// docs/protocols/wing.md section 13 (items 1-3, 14 and every gap the walk
// can close). The client can encode only three requests: the definitions of
// the root's children (DA DD), of a node's children (D7 <hash> DD) and a
// node's data (D7 <hash> DC) (WG-F31, WG-F33). It has no way to send a
// value, a toggle (D8) or a step (D9), and it never changes a model to list
// another model's parameters (wing.md 3.0: WS6's wingschema does, and warns).
package winginfo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mixproto/mixcore/internal/wingproto"
)

// Defaults and limits.
const (
	Port                = 2222 // WG-F10
	DefaultReplyTimeout = 2 * time.Second
	DefaultMaxNodes     = 60000 // the tree has "more than 25000 elements" (wing.md 2.4)
	// MaxChildren caps one definition answer (review sec L6); the widest
	// real node has a few hundred children.
	MaxChildren = 4096
	readBuf     = 16 << 10
	maxPending  = wingproto.MaxDefLen + readBuf
)

// Errors.
var (
	ErrNoReply = errors.New("winginfo: no reply")
	ErrStream  = errors.New("winginfo: unparseable stream")
	ErrTooMany = errors.New("winginfo: more children than any WING node has")
)

// Client is a read-only connection to one console. Not safe for concurrent use.
type Client struct {
	c       net.Conn
	timeout time.Duration
	mux     wingproto.Demux
	data    []byte
	hash    uint32
	hasHash bool
	started bool
}

// NewClient wraps an open TCP connection to the console's port 2222.
func NewClient(c net.Conn, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultReplyTimeout
	}
	return &Client{c: c, timeout: timeout}
}

// request is one of the three read-only requests.
type request struct {
	root bool
	hash uint32
	defs bool // DD; false: DC
}

// encode is the only place bytes for the console are made.
func (r request) encode() []byte {
	var tok []byte
	if r.root {
		tok = wingproto.AppendRoot(tok)
	} else {
		tok = wingproto.AppendHash(tok, r.hash)
	}
	if r.defs {
		tok = wingproto.AppendDefRequest(tok)
	} else {
		tok = wingproto.AppendDataRequest(tok)
	}
	return wingproto.AppendEscaped(nil, tok)
}

func (c *Client) send(r request) error {
	out := r.encode()
	if !c.started {
		out = append(wingproto.AppendSelect(nil, wingproto.ChannelData), out...) // WG-F15
		c.started = true
	}
	if err := c.c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}
	_, err := c.c.Write(out)
	return err
}

// Children returns the definitions of a node's children (root: the tree's
// first level). The answer ends with DE (WG-F30).
func (c *Client) Children(ctx context.Context, root bool, hash uint32) ([]wingproto.Def, error) {
	if err := c.send(request{root: root, hash: hash, defs: true}); err != nil {
		return nil, err
	}
	var defs []wingproto.Def
	for {
		tok, err := c.next(ctx)
		if err != nil {
			return defs, err
		}
		switch tok.Kind {
		case wingproto.KindEnd:
			return defs, nil
		case wingproto.KindDef:
			if len(defs) >= MaxChildren {
				return defs, fmt.Errorf("%w: over %d", ErrTooMany, MaxChildren)
			}
			if d, err := wingproto.ParseDef(tok.Bytes); err == nil {
				defs = append(defs, d)
			}
		}
	}
}

// Value returns the value of one leaf as text; other pushes are skipped.
func (c *Client) Value(ctx context.Context, hash uint32) (string, error) {
	if err := c.send(request{hash: hash}); err != nil {
		return "", err
	}
	for {
		tok, err := c.next(ctx)
		if err != nil {
			return "", err
		}
		if tok.Kind == wingproto.KindHash {
			c.hash, c.hasHash = tok.Hash, true
			continue
		}
		if c.hasHash && isValue(tok.Kind) {
			c.hasHash = false
			if c.hash == hash {
				return valueText(tok), nil
			}
		}
	}
}

func isValue(k wingproto.Kind) bool {
	return k == wingproto.KindInt || k == wingproto.KindFloat || k == wingproto.KindRawFloat || k == wingproto.KindString
}

func valueText(tok wingproto.Token) string {
	switch tok.Kind {
	case wingproto.KindInt:
		return fmt.Sprint(tok.Int)
	case wingproto.KindString:
		return fmt.Sprintf("%q", tok.Bytes)
	case wingproto.KindRawFloat:
		return fmt.Sprintf("raw %g", tok.Float)
	}
	return fmt.Sprintf("%g", tok.Float)
}

// next returns the next data-channel token, reading as needed.
func (c *Client) next(ctx context.Context) (wingproto.Token, error) {
	buf := make([]byte, readBuf)
	for {
		if len(c.data) > 0 {
			tok, n, err := wingproto.Parse(c.data)
			if err == nil {
				out := tok
				out.Bytes = append([]byte(nil), tok.Bytes...)
				c.data = c.data[n:]
				return out, nil
			}
			if !errors.Is(err, wingproto.ErrShort) {
				return wingproto.Token{}, fmt.Errorf("%w: %w", ErrStream, err)
			}
		}
		if ctx.Err() != nil {
			return wingproto.Token{}, ctx.Err()
		}
		if err := c.c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
			return wingproto.Token{}, err
		}
		n, err := c.c.Read(buf)
		c.data = c.mux.Append(c.data, buf[:n], wingproto.ChannelData)
		if len(c.data) > maxPending {
			return wingproto.Token{}, fmt.Errorf("%w: %d bytes without a token", ErrStream, len(c.data))
		}
		if err != nil && n == 0 {
			return wingproto.Token{}, fmt.Errorf("%w: %w", ErrNoReply, err)
		}
	}
}
