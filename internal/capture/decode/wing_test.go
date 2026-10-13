// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

func wingHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWING_DocStream decodes the byte strings of docs/protocols/wing.md 3.6
// and the vendor meter sequence of WG-F51, split at an awkward place.
func TestWING_DocStream(t *testing.T) {
	stream := wingHex(t, "DFD1 D738AE75C2DC D738AE75C2D5C1200000 D7F50F69F801 D7DFDE6A06E2DC DA C2 636831 DD DFD3 D400000002 DCA001DE DFD1")
	if s := (WING{}).Detect(netpkt.TCP, [][]byte{stream}); s != scoreWingSelect {
		t.Fatalf("detect %d", s)
	}
	var got []string
	st := (WING{}).NewStream(netpkt.TCP, stream)
	for _, c := range [][]byte{stream[:9], stream[9:]} {
		st.Feed(c, func(m Message) { got = append(got, m.Key.String()+"="+m.Value.Text) })
	}
	want := []string{"wing #38ae75c2 read=", "wing #38ae75c2=-10", "wing #f50f69f8=1", "wing #df6a06e2 read=", "wing /ch1 defs=", "wing meters=d400000002dca001de"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestWING_UDPDiscovery(t *testing.T) {
	if (WING{}).Detect(netpkt.UDP, [][]byte{[]byte("WING?")}) != scoreWingUDP {
		t.Fatal("discovery not detected")
	}
	var keys []string
	(WING{}).NewStream(netpkt.UDP, nil).Feed([]byte("WING,1.2.3.4,PGM,ngc-full,X,3.0.6"), func(m Message) { keys = append(keys, m.Key.ID) })
	if len(keys) != 1 || keys[0] != "discovery" {
		t.Fatalf("keys %v", keys)
	}
}

func TestWING_RandomBinaryNotClaimed(t *testing.T) {
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*131 + i/7)
	}
	for i := 0; i < len(junk); i += 400 {
		junk[i] = 0xD7 // a few stray D7 bytes
	}
	if s := (WING{}).Detect(netpkt.TCP, [][]byte{junk}); s != 0 {
		t.Fatalf("random binary scored %d", s)
	}
	pushes := wingHex(t, "D738AE75C2D5C1200000 D7F50F69F801 D738AE75C2D5C0400000")
	if s := (WING{}).Detect(netpkt.TCP, [][]byte{pushes}); s != scoreWingTokens {
		t.Fatalf("a mid-stream capture scored %d", s)
	}
}

// TestWING_SelectionCapped (review sec L4): a path built from names never
// grows past wingMaxSel bytes.
func TestWING_SelectionCapped(t *testing.T) {
	stream := []byte{0xDF, 0xD1, 0xDA}
	for range 200 {
		stream = append(stream, 0xCF)
		stream = append(stream, []byte("abcdefghijklmnop")...)
	}
	stream = append(stream, 0xDC)
	var keys []string
	(WING{}).NewStream(netpkt.TCP, nil).Feed(stream, func(m Message) { keys = append(keys, m.Key.ID) })
	if len(keys) != 1 || len(keys[0]) > wingMaxSel+len(" read") {
		t.Fatalf("key of %d bytes", len(keys[0]))
	}
}
