// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package session

import (
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
	"github.com/mixproto/mixcore/internal/capture/netpkt"
	"github.com/mixproto/mixcore/internal/capture/pcapfile"
)

// maxSegment is the largest TCP payload written as one synthesized packet.
const maxSegment = 32 << 10

// Recorder writes traffic that did not come from a packet capture (the relay
// and the introspection client see payloads, not frames) as synthesized raw
// IP packets into a pcap file with the logical endpoints (app and console),
// and hands every frame on for live analysis. Safe for concurrent use.
type Recorder struct {
	mu   sync.Mutex
	w    *pcapfile.Writer
	file string
	seq  map[[2]netip.AddrPort]uint32
	out  func(flow.Frame)
	n    int
}

// NewRecorder writes a pcap header to w; file names the capture in evidence;
// out (may be nil) receives each frame after it is written, outside the
// recorder's lock. out must not block for long: the relay records inline.
func NewRecorder(w io.Writer, file string, out func(flow.Frame)) (*Recorder, error) {
	pw, err := pcapfile.NewWriter(w, netpkt.LinkRaw)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = func(flow.Frame) {}
	}
	return &Recorder{w: pw, file: file, seq: map[[2]netip.AddrPort]uint32{}, out: out}, nil
}

// Datagram records one UDP datagram.
func (r *Recorder) Datagram(at time.Time, src, dst netip.AddrPort, payload []byte) error {
	r.mu.Lock()
	frames, err := r.write(nil, at, netpkt.Packet{Transport: netpkt.UDP, Src: src, Dst: dst, Payload: payload})
	r.mu.Unlock()
	r.handOn(frames)
	return err
}

// Stream records bytes sent on a TCP connection from src to dst. The first
// call for a direction writes a SYN (a SYN-ACK when the other direction was
// seen first) so the capture shows who opened the connection.
func (r *Recorder) Stream(at time.Time, src, dst netip.AddrPort, payload []byte) error {
	r.mu.Lock()
	frames, err := r.stream(at, src, dst, payload)
	r.mu.Unlock()
	r.handOn(frames)
	return err
}

// handOn passes written frames to out after the lock is released, so a slow
// consumer never holds up another goroutine's recording.
func (r *Recorder) handOn(frames []flow.Frame) {
	for _, f := range frames {
		r.out(f)
	}
}

func (r *Recorder) stream(at time.Time, src, dst netip.AddrPort, payload []byte) ([]flow.Frame, error) {
	id := [2]netip.AddrPort{src, dst}
	seq, ok := r.seq[id]
	var frames []flow.Frame
	var err error
	if !ok {
		flags := netpkt.FlagSYN
		if _, reverse := r.seq[[2]netip.AddrPort{dst, src}]; reverse {
			flags |= netpkt.FlagACK
		}
		if frames, err = r.write(frames, at, netpkt.Packet{Transport: netpkt.TCP, Src: src, Dst: dst, Flags: flags}); err != nil {
			return frames, err
		}
		seq = 1
	}
	for len(payload) > 0 {
		n := min(len(payload), maxSegment)
		p := netpkt.Packet{Transport: netpkt.TCP, Src: src, Dst: dst, Seq: seq,
			Flags: netpkt.FlagACK | netpkt.FlagPSH, Payload: payload[:n]}
		if frames, err = r.write(frames, at, p); err != nil {
			return frames, err
		}
		seq += uint32(n)
		payload = payload[n:]
	}
	r.seq[id] = seq
	return frames, nil
}

// write appends p to the pcap and its frame to frames. Callers hold r.mu.
func (r *Recorder) write(frames []flow.Frame, at time.Time, p netpkt.Packet) ([]flow.Frame, error) {
	raw, err := netpkt.Build(p)
	if err != nil {
		return frames, fmt.Errorf("session: record %s -> %s: %w", p.Src, p.Dst, err)
	}
	off, err := r.w.WritePacket(at, raw)
	if err != nil {
		return frames, err
	}
	r.n++
	return append(frames, flow.Frame{At: at, LinkType: netpkt.LinkRaw, Data: raw,
		Evidence: flow.Evidence{File: r.file, Offset: off, Packet: r.n}}), nil
}

// TapConn wraps a connection the tool itself opened (introspection) so that
// both directions are recorded: writes as local -> remote, reads as
// remote -> local.
func TapConn(c net.Conn, rec *Recorder) net.Conn {
	return &tapConn{Conn: c, rec: rec, local: addrPort(c.LocalAddr()), remote: addrPort(c.RemoteAddr())}
}

type tapConn struct {
	net.Conn
	rec           *Recorder
	local, remote netip.AddrPort
}

func (t *tapConn) Read(b []byte) (int, error) {
	n, err := t.Conn.Read(b)
	if n > 0 {
		if rerr := t.rec.Stream(time.Now(), t.remote, t.local, b[:n]); rerr != nil {
			return n, rerr
		}
	}
	return n, err
}

func (t *tapConn) Write(b []byte) (int, error) {
	n, err := t.Conn.Write(b)
	if n > 0 {
		if rerr := t.rec.Stream(time.Now(), t.local, t.remote, b[:n]); rerr != nil {
			return n, rerr
		}
	}
	return n, err
}

func addrPort(a net.Addr) netip.AddrPort {
	if ta, ok := a.(*net.TCPAddr); ok {
		ap := ta.AddrPort()
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.AddrPortFrom(netip.IPv4Unspecified(), 0)
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}
