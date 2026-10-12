// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
)

// Live loop timing and limits.
const (
	tickEvery     = 250 * time.Millisecond
	frameQueue    = 8192
	maxKeysListed = 50
)

const terminalHelp = `commands while recording:
  <label> Enter     mark: keys that change in the next -window are candidates for it
                    end with "= <value>" (e.g. "ch1 fader = -10 dB", "= -inf") to add a law point
  Enter             unlabelled mark
  :s [label]        snapshot (the second one onwards prints the diff to the previous)
  :keys [text]      list keys containing text with their latest values
  :law <key>        fit a value law for key from the labelled marks so far
  :harvest          initial-sync harvests so far
  :q                stop and write the report
`

// command is one parsed terminal line.
type command struct {
	at   time.Time
	verb string // mark, snap, keys, law, harvest, help, quit
	arg  string
}

func parseCommand(at time.Time, line string) command {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, ":") {
		return command{at: at, verb: "mark", arg: line}
	}
	verb, arg, _ := strings.Cut(line[1:], " ")
	arg = strings.TrimSpace(arg)
	switch verb {
	case "s", "snap", "snapshot":
		return command{at: at, verb: "snap", arg: arg}
	case "keys", "k":
		return command{at: at, verb: "keys", arg: arg}
	case "law":
		return command{at: at, verb: "law", arg: arg}
	case "harvest", "h":
		return command{at: at, verb: "harvest"}
	case "q", "quit", "exit":
		return command{at: at, verb: "quit"}
	default:
		return command{at: at, verb: "help"}
	}
}

// readCommands sends each line of in as a command stamped with the time it
// arrived. It ends when in ends or ctx ends after a line; stdin cannot be
// cancelled portably, so in a terminal session the goroutine ends with the
// process.
func readCommands(ctx context.Context, in io.Reader, cmds chan<- command) {
	defer close(cmds)
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		select {
		case cmds <- parseCommand(time.Now(), sc.Text()):
		case <-ctx.Done():
			return
		}
	}
}

// handle runs one command; it returns false for quit.
func (e *engine) handle(c command) bool {
	switch c.verb {
	case "mark":
		label := c.arg
		if label == "" {
			label = fmt.Sprintf("mark %d", len(e.an.Marks())+1)
		}
		e.mark(c.at, label)
	case "snap":
		label := c.arg
		if label == "" {
			label = fmt.Sprintf("snapshot %d", len(e.an.Snapshots())+1)
		}
		e.snapshot(c.at, label)
	case "keys":
		e.listKeys(c.arg)
	case "law":
		e.printLaw(c.arg)
	case "harvest":
		for _, h := range e.an.Harvests() {
			fmt.Fprintf(e.out, "harvest %d from %s: %d keys, closed=%t\n", h.ID, h.App, len(h.Keys), h.Closed)
		}
	case "quit":
		return false
	default:
		fmt.Fprint(e.out, terminalHelp)
	}
	return true
}

func (e *engine) listKeys(filter string) {
	n := 0
	for _, k := range e.an.Keys() {
		if !strings.Contains(k.String(), filter) {
			continue
		}
		if n == maxKeysListed {
			fmt.Fprintln(e.out, "  ... narrow the filter")
			return
		}
		v, _ := e.an.Value(k)
		fmt.Fprintf(e.out, "  %s = %s\n", k, v.Text)
		n++
	}
}

func (e *engine) printLaw(keyText string) {
	k, ok := e.an.FindKey(keyText)
	if !ok {
		fmt.Fprintf(e.out, "law: no key %q (see :keys)\n", keyText)
		return
	}
	pts := e.an.LawPoints(k)
	l, err := fitPoints(pts)
	if err != nil {
		fmt.Fprintf(e.out, "law %s: %v (%d labelled point(s); label marks \"... = <value>\")\n", k, err, len(pts))
		return
	}
	fmt.Fprintf(e.out, "law %s: %s\n", k, l)
}

// liveLoop is the owner loop of a recording: frames from the source,
// commands from the terminal, a ticker for detection windows and mark
// timeouts. It returns when ctx ends, the operator quits, stdin closes after
// :q, or the source fails.
func (e *engine) liveLoop(ctx context.Context, frames <-chan flow.Frame, cmds <-chan command, srcDone <-chan error) error {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case fr := <-frames:
			e.frame(fr)
		case c, ok := <-cmds:
			if !ok {
				cmds = nil // stdin closed: keep recording until Ctrl-C
				continue
			}
			if !e.handle(c) {
				return nil
			}
		case now := <-t.C:
			e.tick(now)
		case err := <-srcDone:
			e.drain(frames)
			return err
		}
	}
}

// drain feeds the frames still queued when the source ended.
func (e *engine) drain(frames <-chan flow.Frame) {
	for {
		select {
		case fr := <-frames:
			e.frame(fr)
		default:
			return
		}
	}
}
