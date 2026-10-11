// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type logRecorder struct {
	mu     sync.Mutex
	lines  []string
	levels []int
	panics bool
}

func (r *logRecorder) Log(level int, line string) {
	r.mu.Lock()
	r.lines = append(r.lines, line)
	r.levels = append(r.levels, level)
	r.mu.Unlock()
	if r.panics {
		panic("sink bug")
	}
}

func (r *logRecorder) snapshot() ([]int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.levels...), append([]string(nil), r.lines...)
}

func TestLogSink_DeliversEngineLinesAtOrAboveTheLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		rec := &logRecorder{}
		e.eng.SetLogSink(rec, LogInfo)
		e.startLive(t)
		synctest.Wait()
		levels, lines := rec.snapshot()
		found := false
		for i, l := range lines {
			if levels[i] < LogInfo {
				t.Fatalf("line below the minimum level: %d %q", levels[i], l)
			}
			if strings.HasPrefix(l, "console: live ") && strings.Contains(l, "endpoint=10.0.0.1") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no 'console: live' line in %q", lines)
		}
		e.eng.SetLogSink(nil, LogInfo) // stops the goroutine (goleak checks)
	})
}

func TestSinkHandler_FormatsAttrsAndGroups(t *testing.T) {
	p := newLogPump()
	rec := &logRecorder{}
	p.set(rec, LogDebug)
	defer p.set(nil, LogDebug)
	log := slog.New(&sinkHandler{pump: p}).With("a", 1).WithGroup("g")
	log.Debug("hello world", "b", "two words")
	waitLines(t, rec, 1)
	levels, lines := rec.snapshot()
	if levels[0] != LogDebug || lines[0] != `hello world a=1 g.b="two words"` {
		t.Fatalf("got %d %q", levels[0], lines[0])
	}
}

func TestLogSink_DisabledCostsNothingAndPanicsAreContained(t *testing.T) {
	p := newLogPump()
	h := &sinkHandler{pump: p}
	if h.Enabled(t.Context(), slog.LevelError) {
		t.Fatal("no sink: nothing is enabled")
	}
	rec := &logRecorder{panics: true}
	p.set(rec, LogWarn)
	defer p.set(nil, LogWarn)
	if h.Enabled(t.Context(), slog.LevelInfo) || !h.Enabled(t.Context(), slog.LevelWarn) {
		t.Fatal("level filter")
	}
	slog.New(h).Warn("one")
	slog.New(h).Warn("two")
	waitLines(t, rec, 2) // the first panic did not stop delivery
}

func TestLogSink_OverflowIsCountedNotQueued(t *testing.T) {
	p := newLogPump()
	for range logQueueCap + 10 {
		p.enqueue(LogInfo, "x")
	}
	rec := &logRecorder{}
	p.set(rec, LogInfo)
	defer p.set(nil, LogInfo)
	waitLines(t, rec, logQueueCap+1)
	_, lines := rec.snapshot()
	if !strings.Contains(strings.Join(lines, "\n"), "10 log lines dropped") {
		t.Fatalf("no drop notice in the last lines %q", lines[len(lines)-2:])
	}
}

func waitLines(t *testing.T, r *logRecorder, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, lines := r.snapshot(); len(lines) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	_, lines := r.snapshot()
	t.Fatalf("got %d lines, want %d", len(lines), n)
}
