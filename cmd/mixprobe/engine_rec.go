// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mixproto/mixcore/api"
	"github.com/mixproto/mixcore/meters"
)

const (
	pollEvery    = 10 * time.Millisecond
	liveWait     = 15 * time.Second
	clientID     = "mixprobe"
	frameAtStart = 16 // capture time offset in a meter frame (meter-frame.md)
)

type engParam struct {
	Strip struct {
		Kind  string `json:"kind"`
		Index int    `json:"index,omitempty"`
	} `json:"strip"`
	Field string `json:"field"`
}

type engChange struct {
	Param  engParam        `json:"param"`
	Value  json.RawMessage `json:"value"`
	Origin struct {
		Kind     string `json:"kind"`
		ClientID string `json:"clientId"`
	} `json:"origin"`
}

type engEvent struct {
	Type     string    `json:"type"`
	Param    *engParam `json:"param"`
	ClientID string    `json:"clientId"`
	Reason   string    `json:"reason"`
}

type engStrip struct {
	Kind    string          `json:"kind"`
	Index   int             `json:"index"`
	Name    string          `json:"name"`
	FaderDB json.RawMessage `json:"faderDB"`
	Mute    bool            `json:"mute"`
}

// engState is the part of a state message (state.schema.json) mixprobe uses.
type engState struct {
	Type    string            `json:"type"`
	Status  string            `json:"status"`
	Limited bool              `json:"limited"`
	Changes []engChange       `json:"changes"`
	Events  []json.RawMessage `json:"events"`
	Strips  []engStrip        `json:"strips"`
}

type recMsg struct {
	at  time.Time
	st  engState
	raw string
}

// engineRec records every state message of an engine with its arrival time.
type engineRec struct {
	mu   sync.Mutex
	msgs []recMsg
}

// OnState implements api.StateListener.
func (r *engineRec) OnState(s string) {
	var st engState
	if json.Unmarshal([]byte(s), &st) != nil {
		return
	}
	r.mu.Lock()
	r.msgs = append(r.msgs, recMsg{at: time.Now(), st: st, raw: s})
	r.mu.Unlock()
}

// since returns the messages that arrived at or after t.
func (r *engineRec) since(t time.Time) []recMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recMsg
	for _, m := range r.msgs {
		if !m.at.Before(t) {
			out = append(out, m)
		}
	}
	return out
}

// waitFor polls until a message after t satisfies match, or wait passes.
func (r *engineRec) waitFor(ctx context.Context, t time.Time, wait time.Duration, match func(recMsg) bool) (recMsg, bool) {
	deadline := time.Now().Add(wait)
	for {
		for _, m := range r.since(t) {
			if match(m) {
				return m, true
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return recMsg{}, false
		}
		time.Sleep(pollEvery)
	}
}

func statusIs(s string) func(recMsg) bool { return func(m recMsg) bool { return m.st.Status == s } }

// eventOf returns the raw events of a message with the given type.
func eventsOf(m recMsg, typ string) []json.RawMessage {
	var out []json.RawMessage
	for _, raw := range m.st.Events {
		var ev engEvent
		if json.Unmarshal(raw, &ev) == nil && ev.Type == typ {
			out = append(out, raw)
		}
	}
	return out
}

// startEngine starts an engine on the console and waits until it is live.
func (p *prober) startEngine(ctx context.Context, metersOn bool) (*api.Engine, *engineRec, error) {
	eng := api.NewEngineWith(api.Options{ListenPacket: p.cfg.listen})
	rec := &engineRec{}
	eng.SetStateListener(rec)
	cfg := fmt.Sprintf(`{"version":0,"family":%q,"host":%q,"port":%d,"meters":{"inputs":%t}}`,
		p.cfg.fam.name, p.cfg.console.Addr().String(), p.cfg.console.Port(), metersOn)
	t0 := time.Now()
	if _, err := eng.Start(cfg); err != nil {
		eng.Close()
		return nil, nil, fmt.Errorf("mixprobe: engine start: %w", err)
	}
	if _, ok := rec.waitFor(ctx, t0, liveWait, statusIs("live")); !ok {
		eng.Close()
		return nil, nil, fmt.Errorf("%w: the engine did not get live within %s", errAborted, liveWait)
	}
	return eng, rec, nil
}

// meterClock samples the engine's newest meter frame and remembers the
// capture time of the newest one seen.
type meterClock struct {
	mu   sync.Mutex
	last time.Time
}

func (c *meterClock) run(ctx context.Context, eng *api.Engine) {
	buf := make([]byte, api.MeterFrameMaxBytes)
	for ctx.Err() == nil {
		if n := eng.ReadMeterFrame(buf); n >= meters.FrameHeaderSize && n <= len(buf) {
			if at := int64(binary.LittleEndian.Uint64(buf[frameAtStart:])); at != 0 {
				c.mu.Lock()
				c.last = time.Unix(0, at)
				c.mu.Unlock()
			}
		}
		time.Sleep(pollEvery)
	}
}

func (c *meterClock) latest() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// gestureCmd builds an Apply command of this tool for input ch.
func gestureCmd(typ string, ch int, field, value string) string {
	v := ""
	if value != "" {
		v = `,"value":` + value
	}
	return fmt.Sprintf(`{"version":0,"type":%q,"param":{"strip":{"kind":"input","index":%d},"field":%q}%s,"origin":{"kind":"gesture","clientId":%q}}`,
		typ, ch, field, v, clientID)
}
