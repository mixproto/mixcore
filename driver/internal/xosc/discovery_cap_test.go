// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package xosc

import (
	"net"
	"os"
	"testing"

	"github.com/mixproto/mixcore/internal/osc"
)

type xinfoFlood struct {
	net.PacketConn
	n, next int
	pkt     []byte
}

func (c *xinfoFlood) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.next >= c.n {
		return 0, nil, os.ErrDeadlineExceeded
	}
	c.next++
	return copy(b, c.pkt), &net.UDPAddr{IP: net.IPv4(10, 1, byte(c.next>>8), byte(c.next)), Port: 10023}, nil
}

// TestCollectReplies_Capped (review sec L7): a flood of /xinfo replies
// lists at most MaxFound consoles.
func TestCollectReplies_Capped(t *testing.T) {
	pkt, err := osc.AppendString(nil, addrXinfo, osc.String("1.2.3.4"), osc.String("X32-1"), osc.String("X32"), osc.String("4.06"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := collectReplies(&xinfoFlood{n: 1000, pkt: pkt})
	if err != nil || len(found) != MaxFound {
		t.Fatalf("%d found, %v", len(found), err)
	}
}
