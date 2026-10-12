// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package flow

import (
	"bytes"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// maxOutOfOrder bounds the segments held while waiting for a missing one.
const maxOutOfOrder = 64

type pendingChunk struct {
	at   time.Time
	dir  Dir
	data []byte
	ev   Evidence
}

type flow struct {
	id       flowID
	dec      decode.Decoder
	pending  []pendingChunk
	streams  [2]decode.Stream
	reset    [2]bool // a TCP gap: start a fresh stream for that direction
	tcp      [2]tcpDir
	messages int
}

func newFlow(id flowID) *flow { return &flow{id: id} }

// accept returns the in-order payload chunks p contributes (copies).
func (f *flow) accept(dir Dir, p netpkt.Packet, st *Stats) [][]byte {
	if p.Transport == netpkt.UDP {
		if len(p.Payload) == 0 {
			return nil
		}
		return [][]byte{bytes.Clone(p.Payload)}
	}
	chunks, gap := f.tcp[dir-1].accept(p.Seq, p.Flags, p.Payload)
	if gap {
		st.Gaps++
		f.reset[dir-1] = true
	}
	return chunks
}

// tcpDir orders one direction of a TCP connection by sequence number.
type tcpDir struct {
	next   uint32
	synced bool
	ooo    map[uint32][]byte
}

// accept returns the in-order data of a segment; gap is true when data was
// lost (the capture missed it) and the decoder must resynchronize.
func (d *tcpDir) accept(seq uint32, flags uint8, payload []byte) (chunks [][]byte, gap bool) {
	if flags&netpkt.FlagSYN != 0 {
		*d = tcpDir{next: seq + 1, synced: true}
		return nil, false
	}
	if len(payload) == 0 {
		return nil, false
	}
	if !d.synced { // capture started mid-connection
		*d = tcpDir{next: seq, synced: true}
	}
	diff := int32(seq - d.next)
	switch {
	case diff < 0:
		if int(-diff) >= len(payload) {
			return nil, false // retransmission of data already seen
		}
		payload = payload[-diff:]
	case diff > 0:
		if len(d.ooo) < maxOutOfOrder {
			if d.ooo == nil {
				d.ooo = map[uint32][]byte{}
			}
			d.ooo[seq] = bytes.Clone(payload)
			return nil, false
		}
		d.next, d.ooo, gap = seq, nil, true // give up on the missing segment
	}
	chunks = append(chunks, bytes.Clone(payload))
	d.next += uint32(len(payload))
	for {
		next, ok := d.ooo[d.next]
		if !ok {
			return chunks, gap
		}
		delete(d.ooo, d.next)
		chunks = append(chunks, next)
		d.next += uint32(len(next))
	}
}
