// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package session

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mixproto/mixcore/internal/capture/flow"
)

// Index record types.
const (
	TypeMessage  = "msg"
	TypeMark     = "mark"
	TypeSnapshot = "snapshot"
	maxIndexLine = 1 << 20
)

// Record is one line of index.jsonl.
type Record struct {
	Type      string    `json:"type"`
	T         time.Time `json:"t"`
	Seq       int       `json:"seq,omitempty"`
	Dir       string    `json:"dir,omitempty"`
	Transport string    `json:"transport,omitempty"`
	App       string    `json:"app,omitempty"`
	Console   string    `json:"console,omitempty"`
	Decoder   string    `json:"decoder,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Key       string    `json:"key,omitempty"`
	Value     *string   `json:"value,omitempty"` // absent: a message without a value
	Num       *float64  `json:"num,omitempty"`
	Raw       string    `json:"raw,omitempty"`
	Packet    int       `json:"pkt,omitempty"`
	Offset    int64     `json:"pcap_offset,omitempty"`
	ID        int       `json:"id,omitempty"`
	Label     string    `json:"label,omitempty"`
}

// IndexWriter appends records to index.jsonl.
type IndexWriter struct {
	w   *bufio.Writer
	c   io.Closer
	enc *json.Encoder
	seq int
}

// NewIndexWriter writes to wc and closes it on Close.
func NewIndexWriter(wc io.WriteCloser) *IndexWriter {
	bw := bufio.NewWriter(wc)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false) // keep "app->console" greppable
	return &IndexWriter{w: bw, c: wc, enc: enc}
}

// Message appends one observation.
func (iw *IndexWriter) Message(o flow.Observation) error {
	iw.seq++
	r := Record{
		Type: TypeMessage, T: o.At, Seq: iw.seq, Dir: o.Dir.String(), Transport: string(o.Transport),
		App: o.App.String(), Console: o.Console.String(), Decoder: o.Decoder, Kind: string(o.Msg.Key.Kind),
		Key: o.Msg.Key.ID, Raw: hex.EncodeToString(o.Msg.Raw), Packet: o.Evidence.Packet, Offset: o.Evidence.Offset,
	}
	if v := o.Msg.Value; v.Present {
		r.Value = &v.Text
		if v.HasNum {
			r.Num = &v.Num
		}
	}
	return iw.write(r)
}

// Mark appends a mark.
func (iw *IndexWriter) Mark(id int, at time.Time, label string) error {
	return iw.write(Record{Type: TypeMark, T: at, ID: id, Label: label})
}

// Snapshot appends a snapshot point.
func (iw *IndexWriter) Snapshot(id int, at time.Time, label string) error {
	return iw.write(Record{Type: TypeSnapshot, T: at, ID: id, Label: label})
}

func (iw *IndexWriter) write(r Record) error {
	if err := iw.enc.Encode(r); err != nil {
		return fmt.Errorf("session: index: %w", err)
	}
	return nil
}

// Flush writes buffered records to the file.
func (iw *IndexWriter) Flush() error { return iw.w.Flush() }

// Close flushes and closes the file.
func (iw *IndexWriter) Close() error { return errors.Join(iw.w.Flush(), iw.c.Close()) }

// Annotations returns the mark and snapshot records of an index, in order;
// a re-analysis replays them at their times.
func Annotations(r io.Reader) ([]Record, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxIndexLine)
	var out []Record
	for line := 1; sc.Scan(); line++ {
		b := sc.Bytes()
		if !mayBeAnnotation(b) {
			continue
		}
		var rec Record
		if err := json.Unmarshal(b, &rec); err != nil {
			return nil, fmt.Errorf("session: index line %d: %w", line, err)
		}
		if rec.Type == TypeMark || rec.Type == TypeSnapshot {
			out = append(out, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("session: index: %w", err)
	}
	return out, nil
}

// mayBeAnnotation skips message lines without decoding them.
func mayBeAnnotation(b []byte) bool {
	const msgPrefix = `{"type":"msg"`
	return len(b) > 0 && string(b[:min(len(b), len(msgPrefix))]) != msgPrefix
}
