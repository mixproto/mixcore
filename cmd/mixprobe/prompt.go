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
	"sync"
)

// Physical step ids. Tests answer them by acting on a simulator.
const (
	stepAmpsOff     = "amps-off"
	stepCloseApps   = "close-apps"
	stepMuteGroupOn = "mute-group-lit"
	stepMoveFader   = "move-fader"
	stepRecallScene = "recall-scene"
	stepUnplug      = "unplug"
	stepReplug      = "replug"
	stepReboot      = "reboot"
)

// Prompter shows an instruction for a physical step and waits for the person
// at the console. Step returns done=false when the person skipped it; Ask
// returns the yes/no answer (answered=false when skipped).
type Prompter interface {
	Step(ctx context.Context, id, text string) (done bool, err error)
	Ask(ctx context.Context, id, question string) (yes, answered bool, err error)
}

// terminal prompts on out and reads answers line by line from in. One reader
// goroutine feeds lines, so a prompt can be cancelled with Ctrl-C.
type terminal struct {
	out   io.Writer
	lines chan string
	once  sync.Once
	in    io.Reader
}

func newTerminal(in io.Reader, out io.Writer) *terminal {
	return &terminal{in: in, out: out, lines: make(chan string)}
}

func (t *terminal) start() {
	t.once.Do(func() {
		go func() { // ends with the process; stdin has no portable cancel
			sc := bufio.NewScanner(t.in)
			for sc.Scan() {
				t.lines <- strings.TrimSpace(sc.Text())
			}
			close(t.lines)
		}()
	})
}

func (t *terminal) line(ctx context.Context) (string, error) {
	t.start()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case l, ok := <-t.lines:
		if !ok {
			return "", io.EOF
		}
		return l, nil
	}
}

// Step implements Prompter.
func (t *terminal) Step(ctx context.Context, id, text string) (bool, error) {
	fmt.Fprintf(t.out, "\n>>> [%s] %s\n    Press Enter when done, or type s and Enter to skip: ", id, text)
	l, err := t.line(ctx)
	if err != nil {
		return false, err
	}
	return !strings.EqualFold(l, "s"), nil
}

// Ask implements Prompter.
func (t *terminal) Ask(ctx context.Context, id, question string) (bool, bool, error) {
	for {
		fmt.Fprintf(t.out, "\n??? [%s] %s\n    Type y or n and Enter (s to skip): ", id, question)
		l, err := t.line(ctx)
		if err != nil {
			return false, false, err
		}
		switch strings.ToLower(l) {
		case "y", "yes":
			return true, true, nil
		case "n", "no":
			return false, true, nil
		case "s":
			return false, false, nil
		}
	}
}
