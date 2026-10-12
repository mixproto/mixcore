// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decode

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mixproto/mixcore/internal/capture/netpkt"
)

// Text limits and scores.
const (
	maxLine          = 64 << 10
	maxTokens        = 64
	scoreText        = 90
	minPrintableText = 0.98
)

// Text decodes newline-terminated text lines (RCP-style command protocols)
// and JSON lines. Over UDP a datagram also ends a line.
//
// Key rule for a text line (a heuristic, no protocol facts): the address is
// the first token containing '/' or ':'. Tokens before it are a verb or a
// status word and are not part of the key, so a request, its reply and an
// unsolicited notification of the same parameter share a key. The last token
// is the value, the tokens between address and value are indexes. A line
// whose verb is exactly "get", or that ends at the address, has no value.
// Without an address token the key is every token but the last.
//
// A line that starts with '{' or '[' and is valid JSON is flattened as JSON.
type Text struct{}

// Name implements Decoder.
func (Text) Name() string { return "text" }

// Detect implements Decoder: printable UTF-8 with line ends.
func (Text) Detect(t netpkt.Transport, samples [][]byte) int {
	all := joined(samples)
	if len(all) == 0 || !utf8.Valid(all) {
		return 0
	}
	printable := 0
	for _, c := range all {
		if c >= 0x20 || c == '\n' || c == '\r' || c == '\t' {
			printable++
		}
	}
	if float64(printable) < minPrintableText*float64(len(all)) {
		return 0
	}
	if t == netpkt.TCP && bytes.IndexByte(all, '\n') < 0 {
		return 0
	}
	return scoreText
}

// NewStream implements Decoder.
func (Text) NewStream(t netpkt.Transport, _ []byte) Stream {
	return &textStream{udp: t == netpkt.UDP, buf: buffer{max: maxLine}}
}

type textStream struct {
	udp bool
	buf buffer
}

func (s *textStream) Feed(chunk []byte, emit func(Message)) {
	if s.udp {
		for line := range bytes.SplitSeq(chunk, []byte{'\n'}) {
			emitLine(line, emit)
		}
		return
	}
	if !s.buf.add(chunk) {
		emitLine(chunk, emit) // an over-long line: decode what this chunk holds
		return
	}
	for {
		i := bytes.IndexByte(s.buf.b, '\n')
		if i < 0 {
			return
		}
		emitLine(s.buf.b[:i], emit)
		s.buf.consume(i + 1)
	}
}

func emitLine(raw []byte, emit func(Message)) {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return
	}
	if line[0] == '{' || line[0] == '[' {
		if flattenJSON([]byte(line), raw, emit) {
			return
		}
	}
	key, val, ok := textKey(Tokenize(line))
	if ok {
		emit(newMessage(Key{KindText, key}, val, raw))
	}
}

// textKey applies the key rule of Text.
func textKey(tok []string) (string, Value, bool) {
	if len(tok) == 0 {
		return "", Value{}, false
	}
	at := -1
	for i, t := range tok {
		if strings.ContainsAny(t, "/:") {
			at = i
			break
		}
	}
	if at < 0 {
		if len(tok) == 1 {
			return tok[0], Value{}, true
		}
		return strings.Join(tok[:len(tok)-1], " "), tokenValue(tok[len(tok)-1]), true
	}
	rest := tok[at:]
	if len(rest) == 1 || (at == 1 && strings.EqualFold(tok[0], "get")) {
		return strings.Join(rest, " "), Value{}, true
	}
	return strings.Join(rest[:len(rest)-1], " "), tokenValue(rest[len(rest)-1]), true
}

func tokenValue(t string) Value {
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return NumValue(f, t)
	}
	return TextValue(t)
}

// Tokenize splits on spaces and tabs; a double-quoted token (with backslash
// escapes) is one token and keeps its quotes.
func Tokenize(line string) []string {
	var out []string
	for i := 0; i < len(line) && len(out) < maxTokens; {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		j := tokenEnd(line, i)
		out = append(out, line[i:j])
		i = j
	}
	return out
}

func tokenEnd(line string, i int) int {
	if line[i] != '"' {
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		return i
	}
	for i++; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(line)
}
