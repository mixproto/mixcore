// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"net"
	"os"
	"testing"
)

// replyConn is a PacketConn that yields n identity replies from n
// addresses, then a deadline.
type replyConn struct {
	net.PacketConn
	n, next int
	payload func(i int) []byte
}

func (c *replyConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.next >= c.n {
		return 0, nil, os.ErrDeadlineExceeded
	}
	c.next++
	ip := net.IPv4(10, 1, byte(c.next>>8), byte(c.next))
	return copy(b, c.payload(c.next)), &net.UDPAddr{IP: ip, Port: DefaultPort}, nil
}

// TestCollectReplies_Capped (review sec L7): a flood of replies lists at
// most maxFound consoles.
func TestCollectReplies_Capped(t *testing.T) {
	pc := &replyConn{n: 1000, payload: func(int) []byte { return []byte("WING,1.2.3.4,PGM,ngc-full,S,3.0.6") }}
	found, err := collectReplies(pc)
	if err != nil || len(found) != maxFound {
		t.Fatalf("%d found, %v", len(found), err)
	}
}
