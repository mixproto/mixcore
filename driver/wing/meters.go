// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/meters"
)

// Meter plan (wing.md section 6): channels 1..40 and mains 1..4 in one
// report id; per channel the input level (pre-fader, for gain staging), per
// main 1 its output. Frame (contracts/core-api/meter-frame.md, family wing):
// levels 0..39 inputs 1..40, 40 Main L, 41 Main R.
const (
	meterChannels = maxInputs
	meterMains    = 4
	meterBlocks   = meterChannels + meterMains
	// meterPacketLen is the only length accepted: 4 + 2 x 8 x 44 = 708
	// bytes (wing.md section 6); anything else is dropped.
	meterPacketLen = wingproto.MeterIDBytes + 2*wingproto.MeterWordsPerStrip*meterBlocks
	meterLevels    = meterChannels + 2
	meterMainBlock = meterChannels // main 1 is the first main block
	// meterIDTag marks our report ids ("MX" in the high half); the low half
	// counts sessions, so a stream still running for an old session is
	// dropped (any 32-bit id works, WG-F54).
	meterIDTag    = 0x4D580000
	meterRxBuffer = 2048
)

// meterBlocksRequested is the block list of every request.
var meterBlocksRequested = func() []wingproto.MeterBlock {
	ch := make([]int, meterChannels)
	for i := range ch {
		ch[i] = i + 1
	}
	return []wingproto.MeterBlock{
		{Type: wingproto.MeterChannel, Indexes: ch},
		{Type: wingproto.MeterMain, Indexes: []int{1, 2, 3, 4}},
	}
}()

// meterSink owns the UDP socket meters arrive on and publishes frames
// (latest wins, no allocation per packet). It lives as long as the link.
type meterSink struct {
	pc      net.PacketConn
	port    uint16
	console netip.Addr
	slot    *meters.Slot

	mu      sync.Mutex // orders publishing with switching off
	enabled atomic.Bool
	id      atomic.Uint32 // report id of the current request; 0 = none
	inputs  atomic.Int32  // inputs of the session; levels above are -inf
	lastAt  atomic.Int64  // unix nanoseconds of the last packet of the current report id
	frame   meters.Frame

	errOnce sync.Once
	err     atomic.Pointer[error]
	done    chan struct{}
	wg      sync.WaitGroup
}

func openMeterSink(ctx context.Context, listen func(ctx context.Context, network, address string) (net.PacketConn, error),
	console netip.Addr, slot *meters.Slot) (*meterSink, error) {
	pc, err := listen(ctx, "udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(0)))
	if err != nil {
		return nil, fmt.Errorf("wing: open meter socket: %w", err)
	}
	ua, ok := pc.LocalAddr().(*net.UDPAddr)
	if !ok || ua.Port <= 0 || ua.Port > math.MaxUint16 {
		_ = pc.Close()
		return nil, fmt.Errorf("wing: meter socket has no UDP port: %v", pc.LocalAddr())
	}
	m := &meterSink{pc: pc, port: uint16(ua.Port), console: console, slot: slot, done: make(chan struct{})}
	safe.Go(&m.wg, m.fail, func() { m.fail(m.readLoop()) })
	return m, nil
}

func (m *meterSink) fail(err error) {
	if err == nil {
		return
	}
	m.errOnce.Do(func() { m.err.Store(&err) })
}

func (m *meterSink) failure() error {
	if p := m.err.Load(); p != nil {
		return *p
	}
	return nil
}

func (m *meterSink) close() {
	select {
	case <-m.done:
		return
	default:
	}
	close(m.done)
	_ = m.pc.Close()
	m.wg.Wait()
}

// switchOn starts publishing for report id (levels of a previous session
// are dropped) or stops it. After switchOn(false, ...) returns, nothing is
// published.
func (m *meterSink) switchOn(on bool, id uint32, inputs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled.Store(on)
	m.id.Store(id)
	m.inputs.Store(int32(inputs))
	if on {
		m.frame.Seq++ // a fresh frame for the new stream
		for i := range m.frame.Levels[:meterLevels] {
			m.frame.Levels[i] = float32(math.Inf(-1))
		}
	}
}

// switchOffIf stops publishing only while id is the current report id, so
// a closing session never switches off its successor's meters.
func (m *meterSink) switchOffIf(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.id.Load() == id {
		m.enabled.Store(false)
		m.id.Store(0)
	}
}

func (m *meterSink) readLoop() error {
	buf := make([]byte, meterRxBuffer)
	for {
		n, from, err := m.pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-m.done:
				return nil
			default:
				return fmt.Errorf("wing: meter socket: %w", err)
			}
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok || ua.AddrPort().Addr().Unmap() != m.console {
			continue // only the console's datagrams (WG-F51: sent to the client IP)
		}
		m.handle(buf[:n], time.Now())
	}
}

// handle decodes one meter datagram of our current report id (WG-F52,
// WG-F53) and publishes it. Wrong ids and lengths are dropped.
func (m *meterSink) handle(pkt []byte, at time.Time) {
	if !m.enabled.Load() || len(pkt) != meterPacketLen {
		return
	}
	id, ok := wingproto.MeterPacketID(pkt)
	if !ok || id != m.id.Load() || id == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled.Load() {
		return
	}
	m.lastAt.Store(at.UnixNano())
	decodeMeterPacket(pkt, &m.frame, int(m.inputs.Load()))
	if m.slot == nil {
		return // no slot: the packet still proves the console is there
	}
	m.frame.Seq++
	m.frame.At = at
	m.slot.Publish(&m.frame)
}

// decodeMeterPacket fills fr from a packet of meterPacketLen bytes: per
// channel the louder of input L and R, then main 1 output L and R.
func decodeMeterPacket(pkt []byte, fr *meters.Frame, inputs int) {
	for ch := range meterChannels {
		lvl := float32(math.Inf(-1))
		if ch < inputs {
			base := ch * wingproto.MeterWordsPerStrip
			l, _ := wingproto.MeterWord(pkt, base+wingproto.MeterInL)
			r, _ := wingproto.MeterWord(pkt, base+wingproto.MeterInR)
			lvl = max(wingproto.MeterDBFS(l), wingproto.MeterDBFS(r))
		}
		fr.Levels[ch] = lvl
	}
	base := meterMainBlock * wingproto.MeterWordsPerStrip
	l, _ := wingproto.MeterWord(pkt, base+wingproto.MeterOutL)
	r, _ := wingproto.MeterWord(pkt, base+wingproto.MeterOutR)
	fr.Levels[meterChannels], fr.Levels[meterChannels+1] = wingproto.MeterDBFS(l), wingproto.MeterDBFS(r)
	fr.Count = meterLevels
}

// errMeterRequest: the meter request could not be encoded (a bug).
var errMeterRequest = errors.New("wing: meter request")

// meterRequest encodes the request of report id to our UDP port (WG-F50).
func (m *meterSink) meterRequest(dst []byte, id uint32) ([]byte, error) {
	out, err := wingproto.AppendMeterRequest(dst, m.port, id, meterBlocksRequested)
	if err != nil {
		return dst, fmt.Errorf("%w: %w", errMeterRequest, err)
	}
	return out, nil
}
