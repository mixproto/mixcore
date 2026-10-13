// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"fmt"
	"slices"
	"time"
)

// Class is what a received request was (the write log, wing.md section 12).
type Class uint8

// Request classes.
const (
	ClassRead  Class = iota + 1 // DC data request
	ClassDefs                   // DD definition request
	ClassWrite                  // a value, click or step token
	ClassMeter                  // a meter request or renewal on DF D3
)

func (c Class) String() string {
	return [...]string{"?", "read", "defs", "write", "meter"}[min(int(c), 4)]
}

// Entry is one logged request.
type Entry struct {
	At      time.Time
	Conn    int // connection number, from 1
	Class   Class
	Path    string
	Value   string
	Applied bool // a write that changed the tree (not refused, locked or read-only)
}

// logEntry appends to the log; callers hold mu.
func (s *Sim) logEntry(cl *client, c Class, path, val string, applied bool) {
	s.log = append(s.log, Entry{At: time.Now(), Conn: cl.id, Class: c, Path: path, Value: val, Applied: applied})
}

// Log returns a copy of every logged request.
func (s *Sim) Log() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

// Writes returns every received write, applied or not: what live-show
// safety tests assert on.
func (s *Sim) Writes() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.log {
		if e.Class == ClassWrite {
			out = append(out, e)
		}
	}
	return out
}

// WriteCount is len(Writes()).
func (s *Sim) WriteCount() int { return len(s.Writes()) }

// Value is the current value of a parameter: a float64 for floats and
// faders (dB), an int for ints, a string for strings and enums.
func (s *Sim) Value(path string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.tree.byPath[path]
	if !ok || !n.isLeaf() {
		return nil, false
	}
	switch {
	case n.val.isFloat:
		return float64(n.val.f), true
	case n.val.isText:
		return n.val.s, true
	default:
		return int(n.val.i), true
	}
}

// SurfaceSet changes a parameter as a person at the console would and
// pushes it, with its derived values, to every connection (WG-F20). v is a
// float64 (dB, Hz ...), an int, a bool or a string. Read-only parameters
// may be set too (the console's own state).
func (s *Sim) SurfaceSet(path string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.tree.byPath[path]
	if !ok || !n.isLeaf() {
		return fmt.Errorf("%w: %s", ErrUnknownPath, path)
	}
	val, err := surfaceValue(n, v)
	if err != nil {
		return err
	}
	changed, derived := s.set(n, val)
	s.pushAll(append(changed, derived...), nil)
	return nil
}

func surfaceValue(n *node, v any) (value, error) {
	switch x := v.(type) {
	case float64:
		if n.typ == typeLinFloat || n.typ == typeLogFloat {
			return floatVal(quantize(n, float32(x))), nil
		}
		return floatVal(float32(x)), nil
	case int:
		return intVal(int32(x)), nil
	case bool:
		return intVal(boolInt(x)), nil
	case string:
		return textVal(x), nil
	}
	return value{}, fmt.Errorf("wing sim: value %T for %s", v, n.path)
}

// SendRaw sends bytes as they are (no mux, no escaping) to every
// connection: a fault, e.g. bytes the protocol does not define (WG-F30).
func (s *Sim) SendRaw(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for cl := range s.conns {
		cl.send(append([]byte(nil), b...))
	}
}

// sceneChannels is how many channel faders a fixture scene sets (at least
// the 8 parameters of the burst rule, wing.md section 7).
const sceneChannels = 12

// RecallScene loads fixture scene n as the console would (WG-F71): channel
// faders 1..12 go to -(5 + n) dB and channel 1 is named "Scene n"; every
// change is pushed as one burst, and with PushSceneMarkers $actidx and
// $active too (item 17).
func (s *Sim) RecallScene(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushAll(s.recallLocked(n), nil)
}

func (s *Sim) recallLocked(n int) []*node {
	var out []*node
	add := func(path string, v value) {
		if node := s.tree.byPath[path]; node != nil {
			c, d := s.set(node, v)
			out = append(append(out, c...), d...)
		}
	}
	for ch := 1; ch <= sceneChannels; ch++ {
		add(fmt.Sprintf("/ch/%d/fdr", ch), floatVal(float32(-5-n)))
	}
	add("/ch/1/name", textVal(fmt.Sprintf("Scene %d", n)))
	markers := map[string]value{"/$ctl/lib/$actidx": intVal(int32(n)), "/$ctl/lib/$active": textVal(fmt.Sprintf("I:SHOW1/scene_%d.snap", n))}
	for path, v := range markers {
		if s.opts.PushSceneMarkers {
			add(path, v)
		} else if node := s.tree.byPath[path]; node != nil {
			node.val = v // the console's state changes; whether it is pushed is item 17
		}
	}
	return out
}

// onAction runs a show-control action written by a client (WG-F71): GO
// loads the scene of $actionidx; the action returns to IDLE.
func (s *Sim) onAction(action string) []*node {
	act := s.tree.byPath["/$ctl/lib/$action"]
	defer func() { act.val = textVal("IDLE") }()
	idx := s.tree.byPath["/$ctl/lib/$actionidx"]
	if action != "GO" || idx == nil || idx.val.i < 1 {
		return nil
	}
	return s.recallLocked(int(idx.val.i))
}
