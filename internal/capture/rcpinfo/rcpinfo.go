// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package rcpinfo asks a Yamaha RCP console for its own parameter and meter
// tables, read-only. Facts: docs/protocols/yamaha-rcp.md (YR-Fnn). The client
// can send only devinfo, prminfo and mtrinfo with one alphanumeric argument;
// any other line is refused before it reaches the socket.
package rcpinfo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/internal/capture/decode"
)

// Protocol facts and limits.
const (
	Port                = 49280 // YR-F01
	DefaultReplyTimeout = 2 * time.Second
	DefaultMaxIndex     = 20000 // table entries asked for at most (T1)
	maxLine             = 64 << 10
	maxSkippedLines     = 1000 // NOTIFY and unrelated lines tolerated per reply
	VerbDevInfo         = "devinfo"
	VerbPrmInfo         = "prminfo"
	VerbMtrInfo         = "mtrinfo"
)

// The only verbs this client sends (read-only rule of yamaha-rcp.md).
var readOnlyVerbs = []string{VerbDevInfo, VerbPrmInfo, VerbMtrInfo}

var argPattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// prmInfoFields names the fields after the address of a prminfo reply (YR-F05).
var prmInfoFields = []string{"x", "y", "min", "max", "default", "unit", "type", "ui", "rw", "scale"}

// Errors.
var (
	ErrNotReadOnly = errors.New("rcpinfo: refusing to send a line that is not a read-only query")
	ErrRemote      = errors.New("rcpinfo: console replied ERROR")
	ErrNoReply     = errors.New("rcpinfo: no reply")
)

// CheckReadOnly returns nil only for "<devinfo|prminfo|mtrinfo> <alnum>".
func CheckReadOnly(line string) error {
	verb, arg, ok := strings.Cut(line, " ")
	if !ok || !slices.Contains(readOnlyVerbs, verb) || !argPattern.MatchString(arg) {
		return fmt.Errorf("%w: %q", ErrNotReadOnly, line)
	}
	return nil
}

// Entry is one row of a parameter or meter table.
type Entry struct {
	Table   string // prminfo or mtrinfo
	Index   int
	Address string
	Fields  []string // tokens after the address, quotes removed
	Raw     string
}

// Field returns a named prminfo field (YR-F05) or "".
func (e Entry) Field(name string) string {
	i := slices.Index(prmInfoFields, name)
	if e.Table != VerbPrmInfo || i < 0 || i >= len(e.Fields) {
		return ""
	}
	return e.Fields[i]
}

// FieldNames are the names Field accepts.
func FieldNames() []string { return slices.Clone(prmInfoFields) }

// Client queries one console connection. Not safe for concurrent use.
type Client struct {
	conn    net.Conn
	r       *bufio.Reader
	timeout time.Duration
	Skipped int // NOTIFY and unrelated lines seen while waiting for replies
}

// NewClient wraps conn; timeout 0 uses DefaultReplyTimeout.
func NewClient(conn net.Conn, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultReplyTimeout
	}
	return &Client{conn: conn, r: bufio.NewReaderSize(conn, maxLine), timeout: timeout}
}

// Query sends one read-only line and returns the tokens of its reply.
func (c *Client) Query(line string) ([]string, string, error) {
	if err := CheckReadOnly(line); err != nil {
		return nil, "", err
	}
	verb, arg, _ := strings.Cut(line, " ")
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, "", fmt.Errorf("rcpinfo: %w", err)
	}
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		return nil, "", fmt.Errorf("rcpinfo: send %q: %w", line, err)
	}
	for range maxSkippedLines {
		raw, err := c.readLine()
		if err != nil {
			return nil, "", fmt.Errorf("%w to %q: %w", ErrNoReply, line, err)
		}
		tok := decode.Tokenize(raw)
		switch {
		case len(tok) >= 2 && tok[0] == "ERROR" && tok[1] == verb:
			return tok, raw, fmt.Errorf("%w: %s", ErrRemote, raw)
		case len(tok) >= 3 && (tok[0] == "OK" || tok[0] == "OKm") && tok[1] == verb && tok[2] == arg:
			return tok, raw, nil
		}
		c.Skipped++ // NOTIFY (YR-F03) or a late reply to an earlier query
	}
	return nil, "", fmt.Errorf("%w to %q: too many unrelated lines", ErrNoReply, line)
}

func (c *Client) readLine() (string, error) {
	b, err := c.r.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// DevInfo returns the value of "devinfo <what>" (YR-F04).
func (c *Client) DevInfo(what string) (string, error) {
	tok, _, err := c.Query(VerbDevInfo + " " + what)
	if err != nil {
		return "", err
	}
	return unquote(strings.Join(tok[3:], " ")), nil
}

// Table reads prminfo or mtrinfo entries from index 0 until the console
// answers ERROR, a reply times out, ctx ends or max entries were read (T1).
// The returned reason says why it stopped.
func (c *Client) Table(ctx context.Context, verb string, max int, progress func(int)) ([]Entry, string, error) {
	if verb != VerbPrmInfo && verb != VerbMtrInfo {
		return nil, "", fmt.Errorf("%w: table %q", ErrNotReadOnly, verb)
	}
	var out []Entry
	for i := range max {
		if ctx.Err() != nil {
			return out, "cancelled", nil
		}
		tok, raw, err := c.Query(verb + " " + strconv.Itoa(i))
		switch {
		case errors.Is(err, ErrRemote):
			return out, "console answered ERROR at index " + strconv.Itoa(i), nil
		case errors.Is(err, ErrNoReply):
			return out, "no reply at index " + strconv.Itoa(i), nil
		case err != nil:
			return out, "", err
		}
		out = append(out, parseEntry(verb, i, tok, raw))
		if progress != nil {
			progress(len(out))
		}
	}
	return out, fmt.Sprintf("index cap %d reached", max), nil
}

func parseEntry(verb string, i int, tok []string, raw string) Entry {
	e := Entry{Table: verb, Index: i, Raw: raw}
	if len(tok) > 3 {
		e.Address = unquote(tok[3])
	}
	for _, f := range tok[min(4, len(tok)):] {
		e.Fields = append(e.Fields, unquote(f))
	}
	return e
}

func unquote(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return strings.Trim(s, `"`)
}
