// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
)

// fakeRCP is a scripted TCP text console for tests: it answers devinfo and
// prminfo from a two-entry table (shapes of docs/protocols/yamaha-rcp.md
// YR-F04, YR-F05), answers get (with a value) and set with OK, and logs
// every line.
type fakeRCP struct {
	mu    sync.Mutex
	lines []string
}

var rcpTable = []string{
	`"MIXER:Current/InCh/Fader/Level" 40 1 -32768 1000 -32768 "dB" integer any rw 100`,
	`"MIXER:Current/InCh/Fader/On" 40 1 0 1 1 "" integer any rw 1`,
}

// serve answers one connection until it closes.
func (c *fakeRCP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := sc.Text()
		c.mu.Lock()
		c.lines = append(c.lines, line)
		c.mu.Unlock()
		if _, err := io.WriteString(conn, rcpReply(line)); err != nil {
			return
		}
	}
}

func rcpReply(line string) string {
	verb, rest, _ := strings.Cut(line, " ")
	switch verb {
	case "devinfo":
		return fmt.Sprintf("OK devinfo %s \"TF5\"\n", rest)
	case "prminfo":
		i, err := strconv.Atoi(rest)
		if err != nil || i >= len(rcpTable) {
			return "ERROR prminfo InvalidArgument\n"
		}
		return fmt.Sprintf("OK prminfo %d %s\n", i, rcpTable[i])
	case "mtrinfo":
		return "ERROR mtrinfo InvalidArgument\n"
	case "get": // a reply carries the value, as YR-F03 replies do
		return "OK " + line + " -32768\n"
	case "set":
		return "OK " + line + "\n"
	default:
		return "ERROR " + verb + " UnknownCommand\n"
	}
}

func (c *fakeRCP) received() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}
