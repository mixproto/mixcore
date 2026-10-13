// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Meter facts (WG-F50..F53).
const (
	meterChannel   = 0xA0
	meterMain      = 0xA3
	meterV2Channel = 0xAB
	meterV2Main    = 0xAE
	meterTypeLast  = 0xAF
	wordsStrip     = 8       // WG-F53; V2 blocks have 11
	noInputWord    = -0x644E // 9bb2: -100.3 dB, an input with no signal (WG-F52)
	silenceWord    = math.MinInt16
	dbScale        = 256 // levels are 1/256 dB (WG-F52)
	discoveryAsk   = "WING?"
	udpBuf         = 2048
)

// meterSub is one connection's meter request (WG-F50).
type meterSub struct {
	port    uint16
	id      uint32
	hasID   bool
	blocks  []meterBlock
	renewed time.Time
}

type meterBlock struct {
	typ   byte
	index int // 1-based
}

// meterLevels are the levels the simulator reports, in dB.
type meterLevels struct {
	in   [channels]float64
	main [mains][2]float64
}

func newMeterLevels() meterLevels {
	var l meterLevels
	for i := range l.in {
		l.in[i] = math.Inf(-1)
	}
	for i := range l.main {
		l.main[i] = [2]float64{math.Inf(-1), math.Inf(-1)}
	}
	return l
}

// SetInputLevel sets the input level of channel ch (1..40) in dBFS.
func (s *Sim) SetInputLevel(ch int, db float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch >= 1 && ch <= channels {
		s.levels.in[ch-1] = db
	}
}

// SetMainLevels sets the output level of main 1 in dBFS.
func (s *Sim) SetMainLevels(l, r float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.levels.main[0] = [2]float64{l, r}
}

// processMeter parses the meter channel (WG-F50): D3 <u16 port>, D4 <u32
// id> (alone: a renewal, WG-F51), DC then type bytes each followed by
// index bytes 00..7F, then DE. It returns the bytes used.
func (s *Sim) processMeter(cl *client, b []byte) int {
	used := 0
	for used < len(b) {
		n := s.meterToken(cl, b[used:])
		if n == 0 {
			return used
		}
		used += n
	}
	return used
}

func (s *Sim) meterToken(cl *client, b []byte) int {
	switch b[0] {
	case 0xD3:
		if len(b) < 3 {
			return 0
		}
		cl.sub.port = binary.BigEndian.Uint16(b[1:3])
		return 3
	case 0xD4:
		if len(b) < 5 {
			return 0
		}
		cl.sub.id, cl.sub.hasID, cl.sub.renewed = binary.BigEndian.Uint32(b[1:5]), true, time.Now()
		s.logEntry(cl, ClassMeter, "renew", fmt.Sprint(cl.sub.id), false)
		return 5
	case 0xDC:
		end := bytes.IndexByte(b, 0xDE)
		if end < 0 {
			return 0
		}
		cl.sub.blocks = parseBlocks(b[1:end])
		s.logEntry(cl, ClassMeter, "request", fmt.Sprint(len(cl.sub.blocks)), false)
		return end + 1
	}
	return 1 // anything else on the meter channel is ignored
}

func parseBlocks(b []byte) []meterBlock {
	var out []meterBlock
	typ := byte(0)
	for _, c := range b {
		switch {
		case c >= meterChannel && c <= meterTypeLast:
			typ = c
			if c == 0xA9 || c == 0xAA { // monitor and RTA have no index (WG-F50)
				out = append(out, meterBlock{typ: c})
			}
		case c <= 0x7F && typ != 0:
			out = append(out, meterBlock{typ: typ, index: int(c) + 1})
		}
	}
	return out
}

// ServeUDP answers discovery on pc (WG-F11) and sends meter packets from it
// (WG-F51) until ctx ends.
func (s *Sim) ServeUDP(ctx context.Context, pc net.PacketConn) error {
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()
	done := make(chan struct{})
	var wg sync.WaitGroup
	defer func() { close(done); wg.Wait() }()
	wg.Go(func() { s.meterLoop(done) })
	buf := make([]byte, udpBuf)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if string(buf[:n]) == discoveryAsk && !s.isPaused() {
			reply := fmt.Sprintf("WING,%s,%s,%s,%s,%s", s.reportIP(pc.LocalAddr()), s.opts.Name, s.opts.Model, s.opts.Serial, s.opts.Firmware)
			_, _ = pc.WriteTo([]byte(reply), from)
		}
	}
}

func (s *Sim) meterLoop(done <-chan struct{}) {
	t := time.NewTicker(s.opts.MeterInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			s.sendMeters(now)
		}
	}
}

// sendMeters sends one packet per live subscription: alive for MeterTTL
// after the last D4 (WG-F51), to the connection's IP and declared port.
func (s *Sim) sendMeters(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pc == nil || s.paused {
		return
	}
	for cl := range s.conns {
		sub := cl.sub
		if !sub.hasID || sub.port == 0 || len(sub.blocks) == 0 || now.Sub(sub.renewed) > s.opts.MeterTTL || !cl.remote.IsValid() {
			continue
		}
		pkt := s.meterPacket(sub)
		_, _ = s.pc.WriteTo(pkt, net.UDPAddrFromAddrPort(netip.AddrPortFrom(cl.remote, sub.port)))
	}
}

// meterPacket is the report id then int16 BE words per block (WG-F52, WG-F53).
func (s *Sim) meterPacket(sub meterSub) []byte {
	pkt := binary.BigEndian.AppendUint32(nil, sub.id)
	for _, b := range sub.blocks {
		for _, w := range s.blockWords(b) {
			pkt = binary.BigEndian.AppendUint16(pkt, uint16(w))
		}
	}
	return pkt
}

// blockWords: channel and main blocks carry input L/R, output L/R (input
// plus fader for a channel), gate key/gain, dyn key/gain; V2 blocks add
// gate LED, dyn state and automix gain (WG-F53). Other types are silent.
func (s *Sim) blockWords(b meterBlock) []int16 {
	in, out := math.Inf(-1), [2]float64{math.Inf(-1), math.Inf(-1)}
	switch b.typ {
	case meterChannel, meterV2Channel:
		if b.index >= 1 && b.index <= channels {
			in = s.levels.in[b.index-1]
			if f := s.tree.byPath[fmt.Sprintf("/ch/%d/fdr", b.index)]; f != nil {
				out = [2]float64{in + float64(f.val.f), in + float64(f.val.f)}
			}
		}
	case meterMain, meterV2Main:
		if b.index >= 1 && b.index <= mains {
			out = s.levels.main[b.index-1]
			in = out[0]
		}
	}
	inW := word(in, noInputWord)
	w := []int16{inW, inW, word(out[0], silenceWord), word(out[1], silenceWord), inW, 0, inW, 0}
	if b.typ >= meterV2Channel {
		w = []int16{w[0], w[1], w[2], w[3], w[4], w[5], 0, w[6], w[7], 0, 0}
	}
	if b.typ != meterChannel && b.typ != meterMain && b.typ < meterV2Channel {
		return make([]int16, wordsStrip) // other strips: zero levels in the fixture
	}
	return w
}

// word encodes dB in 1/256 dB; -inf and anything below the int16 range is floor.
func word(db float64, floor int16) int16 {
	v := math.Round(db * dbScale)
	if math.IsNaN(v) || v <= math.MinInt16 {
		return floor
	}
	return int16(math.Min(v, math.MaxInt16))
}
