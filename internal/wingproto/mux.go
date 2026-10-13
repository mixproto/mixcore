// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package wingproto is the codec of the WING native protocol on TCP 2222,
// written from docs/protocols/wing.md (fact IDs WG-Fnn): the 14-channel byte
// stream mux with its 0xDF escape (WG-F13, WG-F14), the value and navigation
// tokens of the data channel (WG-F30, WG-F31), node definition responses
// (WG-F33) and the meter request and packet formats (WG-F50..F53).
//
// Every decoder here reads untrusted bytes: it bounds-checks before each read,
// never panics, caps every length an attacker chooses and reports an
// incomplete input with ErrShort so that stream readers can wait for more.
// Encoders are append-style and allocate only when dst must grow.
//
// The simulator (sim/wing) does not use this package: it has its own codec,
// so that a bug here cannot hide behind the same bug in the oracle.
package wingproto

// Mux bytes (WG-F13, WG-F14).
const (
	// Escape starts a channel selection or an escaped 0xDF.
	Escape byte = 0xDF
	// selectBase + n selects channel n (DF D0 .. DF DD).
	selectBase byte = 0xD0
	// escLiteral after Escape is a literal 0xDF in the data.
	escLiteral byte = 0xDE
	// MaxChannel is the highest channel id (14 channels, 0..13).
	MaxChannel = 13
	// ChannelData carries every parameter request and push: DF D1 (WG-F14).
	ChannelData = 1
	// ChannelMeters carries meter requests: DF D3 (WG-F14, WG-F50).
	ChannelMeters = 3
)

// AppendSelect appends the selection of channel ch (0..MaxChannel): DF D0+ch.
// A channel out of range selects the data channel.
func AppendSelect(dst []byte, ch int) []byte {
	if ch < 0 || ch > MaxChannel {
		ch = ChannelData
	}
	return append(dst, Escape, selectBase+byte(ch))
}

// AppendEscaped appends payload with every 0xDF sent as DF DE. WG-F13 says a
// receiver understands that form for every data 0xDF, and WG-F103 shows why
// floats and strings need it too (1790.5 Hz is 44 DF D0 00).
func AppendEscaped(dst, payload []byte) []byte {
	for _, b := range payload {
		if b == Escape {
			dst = append(dst, Escape, escLiteral)
			continue
		}
		dst = append(dst, b)
	}
	return dst
}

// Demux splits the received byte stream into channel data (WG-F13). The
// zero value is ready: before the console selects a channel its bytes count
// as data-channel bytes (a design choice; WS6 processes them alike). It keeps
// its escape state across chunks, so a DF at the end of one TCP read and its
// second byte in the next are handled.
type Demux struct {
	selected bool
	channel  int
	escaped  bool
}

// Channel returns the channel the next data byte belongs to.
func (d *Demux) Channel() int {
	if !d.selected {
		return ChannelData
	}
	return d.channel
}

// Append appends to dst the data bytes of chunk that belong to channel want
// and returns it. Selections change the channel; DF DE is a literal DF; DF
// followed by any other byte is a literal DF and that byte (WG-F13, the
// vendor receive routine).
func (d *Demux) Append(dst, chunk []byte, want int) []byte {
	for _, b := range chunk {
		if !d.escaped {
			if b == Escape {
				d.escaped = true
				continue
			}
			if d.Channel() == want {
				dst = append(dst, b)
			}
			continue
		}
		d.escaped = false
		switch {
		case b == escLiteral:
			dst = d.keep(dst, want, Escape)
		case b >= selectBase && b < escLiteral:
			d.selected, d.channel = true, int(b-selectBase)
		default:
			dst = d.keep(dst, want, Escape, b)
		}
	}
	return dst
}

func (d *Demux) keep(dst []byte, want int, b ...byte) []byte {
	if d.Channel() == want {
		dst = append(dst, b...)
	}
	return dst
}
