// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// JSON limits and scores.
const (
	maxJSONDepth    = 32
	maxJSONLeaves   = 4096
	scoreJSON       = 70
	minJSONCoverage = 0.5
)

// Field names that make an object a keyed parameter (a heuristic): an object
// with a string address field and a value field is reported under the
// address, e.g. {"path":"line/ch1/volume","value":0.5} -> key
// "line/ch1/volume" = 0.5, instead of "$.value" for every parameter.
var (
	jsonAddrFields  = []string{"path", "address", "addr", "param", "parameter", "key"}
	jsonValueFields = []string{"value", "val"}
)

// JSON decodes JSON objects found anywhere in a byte stream, also inside
// binary framing (bytes outside objects are skipped). Keys are JSON paths
// such as $.channels[0].fader, or the address of a keyed object.
type JSON struct{}

// Name implements Decoder.
func (JSON) Name() string { return "json" }

// Detect implements Decoder: at least one object, and objects cover at least
// half of the bytes.
func (JSON) Detect(_ netpkt.Transport, samples [][]byte) int {
	all := joined(samples)
	covered, n := 0, 0
	var sc objectScanner
	sc.feed(all, func(obj []byte) {
		if json.Valid(obj) {
			covered += len(obj)
			n++
		}
	})
	if n == 0 || float64(covered) < minJSONCoverage*float64(len(all)) {
		return 0
	}
	return scoreJSON
}

// NewStream implements Decoder.
func (JSON) NewStream(netpkt.Transport, []byte) Stream { return &jsonStream{} }

type jsonStream struct{ sc objectScanner }

func (s *jsonStream) Feed(chunk []byte, emit func(Message)) {
	s.sc.feed(chunk, func(obj []byte) { flattenJSON(obj, obj, emit) })
}

// objectScanner finds top-level {...} objects in a byte stream. Its state
// (nesting depth, inside a string, after a backslash) survives between
// feeds, so every byte is looked at once: linear in the stream, whatever the
// chunking. Bytes outside objects are skipped. An object deeper than
// maxJSONDepth or longer than maxPendingJSON is abandoned and the scan
// resumes after it.
type objectScanner struct {
	obj   []byte // the pending object so far; empty while searching
	depth int
	inStr bool
	esc   bool
}

// maxPendingJSON caps the bytes held for one unfinished object.
const maxPendingJSON = 64 << 10

func (sc *objectScanner) feed(b []byte, fn func([]byte)) {
	for _, c := range b {
		if sc.depth == 0 {
			if c == '{' {
				sc.obj, sc.depth = append(sc.obj[:0], c), 1
			}
			continue
		}
		sc.obj = append(sc.obj, c)
		if sc.step(c) {
			fn(sc.obj)
			sc.reset()
			continue
		}
		if sc.depth > maxJSONDepth || len(sc.obj) > maxPendingJSON {
			sc.reset()
		}
	}
}

// step advances the state by one byte of an object; true when it closed.
func (sc *objectScanner) step(c byte) bool {
	switch {
	case sc.esc:
		sc.esc = false
	case sc.inStr && c == '\\':
		sc.esc = true
	case c == '"':
		sc.inStr = !sc.inStr
	case sc.inStr:
	case c == '{' || c == '[':
		sc.depth++
	case c == '}' || c == ']':
		sc.depth--
		return sc.depth == 0
	}
	return false
}

func (sc *objectScanner) reset() {
	sc.obj, sc.depth, sc.inStr, sc.esc = sc.obj[:0], 0, false, false
}

// flattenJSON emits one message per leaf of doc; false if doc is not JSON.
func flattenJSON(doc, raw []byte, emit func(Message)) bool {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return false
	}
	leaves := 0
	walkJSON(v, "$", 0, func(path string, val Value) {
		if leaves < maxJSONLeaves {
			leaves++
			emit(newMessage(Key{KindJSON, path}, val, raw))
		}
	})
	return true
}

func walkJSON(v any, path string, depth int, fn func(string, Value)) {
	if depth > maxJSONDepth {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		walkObject(x, path, depth, fn)
	case []any:
		for i, e := range x {
			walkJSON(e, fmt.Sprintf("%s[%d]", path, i), depth+1, fn)
		}
	default:
		fn(path, jsonLeaf(x))
	}
}

func walkObject(obj map[string]any, path string, depth int, fn func(string, Value)) {
	skip := map[string]bool{}
	if addr, af, vf, ok := keyedObject(obj); ok {
		at := addr
		if path != "$" {
			at = path + "/" + addr
		}
		walkJSON(obj[vf], at, depth+1, fn)
		skip[af], skip[vf] = true, true
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if !skip[k] {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		walkJSON(obj[k], path+"."+k, depth+1, fn)
	}
}

func keyedObject(obj map[string]any) (addr, addrField, valueField string, ok bool) {
	for _, af := range jsonAddrFields {
		s, isStr := obj[af].(string)
		if !isStr || s == "" {
			continue
		}
		for _, vf := range jsonValueFields {
			if _, has := obj[vf]; has {
				return s, af, vf, true
			}
		}
	}
	return "", "", "", false
}

func jsonLeaf(v any) Value {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return TextValue(x.String())
		}
		return NumValue(f, x.String())
	case string:
		return TextValue(strconv.Quote(x))
	case bool:
		if x {
			return NumValue(1, "true")
		}
		return NumValue(0, "false")
	default:
		return TextValue("null")
	}
}
