// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mixproto/mixcore/internal/safe"
)

// LogSink receives the engine's log lines (os_log, Logcat, a file). Log is
// called from one dedicated goroutine, never from a session goroutine, so a
// slow sink cannot stall a console connection. It must return quickly.
type LogSink interface {
	Log(level int, line string)
}

// Log levels passed to LogSink.Log and accepted as minLevel (log/slog values).
const (
	LogDebug = int(slog.LevelDebug)
	LogInfo  = int(slog.LevelInfo)
	LogWarn  = int(slog.LevelWarn)
	LogError = int(slog.LevelError)
)

// logQueueCap bounds lines waiting for the sink; beyond it lines are dropped
// and counted, never queued without limit.
const logQueueCap = 256

type logLine struct {
	level int
	text  string
}

type sinkBox struct{ s LogSink }

// logPump delivers log lines to the sink from its own goroutine.
type logPump struct {
	sink    atomic.Pointer[sinkBox]
	min     atomic.Int64
	dropped atomic.Uint64
	lines   chan logLine

	mu   sync.Mutex // guards quit and the goroutine's lifecycle
	quit chan struct{}
	wg   sync.WaitGroup
}

func newLogPump() *logPump { return &logPump{lines: make(chan logLine, logQueueCap)} }

// set installs (or, with nil, removes) the sink. The delivery goroutine runs
// only while a sink is installed.
func (p *logPump) set(sink LogSink, minLevel int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.min.Store(int64(minLevel))
	if sink == nil {
		p.sink.Store(nil)
		p.stopLocked()
		return
	}
	p.sink.Store(&sinkBox{s: sink})
	if p.quit == nil {
		p.quit = make(chan struct{})
		quit := p.quit
		safe.Go(&p.wg, nil, func() { p.run(quit) })
	}
}

func (p *logPump) stopLocked() {
	if p.quit == nil {
		return
	}
	close(p.quit)
	p.quit = nil
	p.wg.Wait()
}

func (p *logPump) enabled(level slog.Level) bool {
	return p.sink.Load() != nil && int64(level) >= p.min.Load()
}

func (p *logPump) enqueue(level int, text string) {
	select {
	case p.lines <- logLine{level: level, text: text}:
	default:
		p.dropped.Add(1)
	}
}

func (p *logPump) run(quit <-chan struct{}) {
	for {
		select {
		case l := <-p.lines:
			p.deliver(l)
		case <-quit:
			return
		}
		if n := p.dropped.Swap(0); n > 0 {
			p.deliver(logLine{level: LogWarn, text: fmt.Sprintf("api: %d log lines dropped (sink too slow)", n)})
		}
	}
}

// deliver calls the sink; a panic in app code is contained here.
func (p *logPump) deliver(l logLine) {
	box := p.sink.Load()
	if box == nil {
		return
	}
	defer func() { _ = recover() }()
	box.s.Log(l.level, l.text)
}

// sinkHandler is a slog.Handler that formats records as one text line
// ("message key=value ...") and hands them to the pump. Records below the
// sink's level, or without a sink, cost one atomic load.
type sinkHandler struct {
	pump *logPump
	wrap func(slog.Handler) slog.Handler // WithAttrs/WithGroup applied so far
}

var _ slog.Handler = (*sinkHandler)(nil)

func (h *sinkHandler) Enabled(_ context.Context, level slog.Level) bool { return h.pump.enabled(level) }

func (h *sinkHandler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	var th slog.Handler = slog.NewTextHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: dropBuiltins})
	if h.wrap != nil {
		th = h.wrap(th)
	}
	if err := th.Handle(ctx, r); err != nil {
		return err
	}
	line := strings.TrimSuffix(b.String(), "\n")
	if line != "" {
		line = r.Message + " " + line
	} else {
		line = r.Message
	}
	h.pump.enqueue(int(r.Level), line)
	return nil
}

func (h *sinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(x slog.Handler) slog.Handler { return x.WithAttrs(attrs) })
}

func (h *sinkHandler) WithGroup(name string) slog.Handler {
	return h.with(func(x slog.Handler) slog.Handler { return x.WithGroup(name) })
}

func (h *sinkHandler) with(step func(slog.Handler) slog.Handler) *sinkHandler {
	prev := h.wrap
	return &sinkHandler{pump: h.pump, wrap: func(x slog.Handler) slog.Handler {
		if prev != nil {
			x = prev(x)
		}
		return step(x)
	}}
}

// dropBuiltins removes time, level and message: the sink gets the level as a
// number, adds its own timestamp, and the message leads the line.
func dropBuiltins(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
		return slog.Attr{}
	}
	return a
}
