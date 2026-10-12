// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package api is the in-process facade every app uses: Start/Stop/Pause/
// Resume, a JSON state listener, Apply for user gestures, MeterFrame and the
// family's fader law, Discover with a result listener, a log sink and the
// Android socket binder (contracts/core-api). The exported surface of Engine
// uses only types gomobile can bind (string, []byte, bool, int, float64, error
// and callback interfaces of those), so core/mobile and core/capi stay thin.
// Go-only extras (NewEngineWith, Options, Close, ReadMeterFrame) are for Go
// hosts, the c-shared binding and tests.
//
// No method blocks on the network: connecting runs in the background and is
// reported through the StateListener.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/meters"
)

// StateListener receives state messages (contracts/core-api/state.schema.json)
// from one goroutine, in order. It must return quickly.
type StateListener interface {
	OnState(stateJSON string)
}

// Options are Go-only settings for hosts and tests (not part of the bound API).
type Options struct {
	// Drivers by family name; nil uses the built-in x32 driver.
	Drivers map[string]driver.Driver
	// ListenPacket opens every socket; nil: OS sockets through SetSocketBinder.
	ListenPacket driver.ListenPacketFunc
	// Logger; nil: records go to the LogSink set with SetLogSink.
	Logger *slog.Logger
	// WriteAudit receives every write a driver sends (tests, diagnostics).
	WriteAudit func(driver.WriteRecord)
	// RereadEvery is the background re-read interval (console.Config); zero: default.
	RereadEvery time.Duration
}

type listenerBox struct{ l StateListener }

type lawBox struct{ law driver.FaderLaw }

// Engine is one console connection behind the in-process API.
type Engine struct {
	opts     Options
	slot     *meters.Slot
	law      atomic.Pointer[lawBox]
	lis      atomic.Pointer[listenerBox]
	wantFull atomic.Bool
	kick     chan struct{}
	logs     *logPump
	binder   atomic.Pointer[binderBox]
	disc     discovery

	mu       sync.Mutex
	sup      *console.Supervisor
	cfg      configJSON
	stopDisp context.CancelFunc
	dispWG   sync.WaitGroup
}

// NewEngine returns an engine with the built-in drivers.
func NewEngine() *Engine { return NewEngineWith(Options{}) }

// NewEngineWith returns an engine with Go-only options.
func NewEngineWith(opts Options) *Engine {
	if opts.Drivers == nil {
		opts.Drivers = map[string]driver.Driver{familyX32: x32.New(x32.Config{})}
	}
	e := &Engine{slot: &meters.Slot{}, kick: make(chan struct{}, 1), logs: newLogPump()}
	if opts.Logger == nil {
		opts.Logger = slog.New(&sinkHandler{pump: e.logs})
	}
	if opts.ListenPacket == nil {
		opts.ListenPacket = e.listenPacket
	}
	e.opts = opts
	e.disc.ctx, e.disc.cancel = context.WithCancel(context.Background())
	if d, ok := opts.Drivers[familyX32]; ok {
		e.law.Store(&lawBox{law: d.FaderLaw()})
	}
	return e
}

type sessionJSON struct {
	Version  int    `json:"version"`
	Family   string `json:"family"`
	Endpoint struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"endpoint"`
	FaderLaw string `json:"faderLaw"`
}

// Start begins connecting to the console described by configJSON and returns
// the session description at once.
func (e *Engine) Start(configJSON string) (out string, err error) {
	defer safe.RecoverAsError(&err)
	defer func() { err = mapError(err) }()
	cfg, err := parseConfig(configJSON)
	if err != nil {
		return "", err
	}
	drv, ok := e.opts.Drivers[cfg.Family]
	if !ok {
		return "", codeErr(CodeInvalidConfig, fmt.Errorf("no driver for family %q", cfg.Family))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sup != nil {
		return "", codeErr(CodeAlreadyStarted, nil)
	}
	sup, err := console.New(console.Config{
		Driver:      drv,
		Endpoint:    driver.Endpoint{Host: cfg.Host, Port: cfg.Port},
		Options:     driver.Options{Logger: e.opts.Logger, Meters: e.slot, ListenPacket: e.opts.ListenPacket, WriteAudit: e.opts.WriteAudit},
		Meters:      driver.MeterRequest{Inputs: cfg.Meters.Inputs},
		Logger:      e.opts.Logger,
		RereadEvery: e.opts.RereadEvery,
	})
	if err != nil {
		return "", err
	}
	e.law.Store(&lawBox{law: drv.FaderLaw()})
	e.sup, e.cfg = sup, cfg
	e.startDispatcher(sup)
	if err := sup.Start(context.Background()); err != nil {
		e.stopDisp()
		e.dispWG.Wait()
		e.sup, e.stopDisp = nil, nil
		return "", err
	}
	return describe(cfg, drv)
}

func describe(cfg configJSON, drv driver.Driver) (string, error) {
	var d sessionJSON
	d.Family, d.FaderLaw = cfg.Family, string(drv.Family())
	d.Endpoint.Host, d.Endpoint.Port = cfg.Host, cfg.Port
	if d.Endpoint.Port == 0 && cfg.Family == familyX32 {
		d.Endpoint.Port = x32.DefaultPort
	}
	b, err := json.Marshal(d)
	return string(b), err
}

// Stop disconnects and forgets the console. Start may be called again.
func (e *Engine) Stop() (err error) {
	defer safe.RecoverAsError(&err)
	e.mu.Lock()
	sup, stopDisp := e.sup, e.stopDisp
	e.sup, e.stopDisp = nil, nil
	e.mu.Unlock()
	if sup == nil {
		return codeErr(CodeNotStarted, nil)
	}
	sup.Stop()
	stopDisp()
	e.dispWG.Wait()
	e.slot.Reset()
	return nil
}

// Pause ends subscriptions and meters (app going to the background). The last
// state stays, with status "stopped". It never writes.
func (e *Engine) Pause() (err error) {
	defer safe.RecoverAsError(&err)
	sup, err := e.current()
	if err != nil {
		return err
	}
	sup.Pause() // keeps the sockets, so a quick Resume is not a new client to the console
	e.slot.Reset()
	return nil
}

// Resume reconnects and re-reads the full state from the console. It never writes.
func (e *Engine) Resume() (err error) {
	defer safe.RecoverAsError(&err)
	sup, err := e.current()
	if err != nil {
		return err
	}
	if err := sup.Start(context.Background()); err != nil && !errors.Is(err, console.ErrAlreadyRunning) {
		return mapError(err)
	}
	return nil
}

func (e *Engine) current() (*console.Supervisor, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sup == nil {
		return nil, codeErr(CodeNotStarted, nil)
	}
	return e.sup, nil
}

// SetStateListener sets (or clears, with nil) the listener. The next message
// it receives is a full snapshot.
func (e *Engine) SetStateListener(l StateListener) {
	if l == nil {
		e.lis.Store(nil)
		return
	}
	e.lis.Store(&listenerBox{l: l})
	e.requestFull()
}

// RequestSnapshot makes the next listener message a full snapshot.
func (e *Engine) RequestSnapshot() (err error) {
	defer safe.RecoverAsError(&err)
	if _, err := e.current(); err != nil {
		return err
	}
	e.requestFull()
	return nil
}

func (e *Engine) requestFull() {
	e.wantFull.Store(true)
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// Apply carries one user gesture (contracts/core-api/command.schema.json).
// It returns at once; the write leaves from the session goroutine.
func (e *Engine) Apply(commandJSON string) (err error) {
	defer safe.RecoverAsError(&err)
	defer func() { err = mapError(err) }()
	cmd, err := parseCommand(commandJSON)
	if err != nil {
		return err
	}
	sup, err := e.current()
	if err != nil {
		return err
	}
	switch cmd.kind {
	case cmdHoldBegin, cmdHoldEnd:
		return sup.Hold(cmd.param, cmd.origin, cmd.kind == cmdHoldBegin)
	default:
		return sup.Apply(driver.Command{Param: cmd.param, Value: cmd.value, Origin: cmd.origin})
	}
}

// MeterFrame returns the newest meter frame (contracts/core-api/meter-frame.md)
// in a new slice. It never blocks on the network or on the session.
func (e *Engine) MeterFrame() (out []byte) {
	defer func() {
		if recover() != nil {
			out = meters.AppendBinary(nil, &meters.Frame{})
		}
	}()
	var fr meters.Frame
	e.slot.CopyLatest(&fr)
	return meters.AppendBinary(make([]byte, 0, meters.EncodedSize(&fr)), &fr)
}

// MeterFrameMaxBytes is the largest frame MeterFrame and ReadMeterFrame return.
const MeterFrameMaxBytes = meters.MaxEncodedSize

// ReadMeterFrame copies the newest meter frame into dst without allocating and
// returns its length. When dst is shorter than that length nothing is copied:
// call again with a buffer of at least the returned size (MeterFrameMaxBytes
// always fits). It never blocks on the network or on the session.
func (e *Engine) ReadMeterFrame(dst []byte) (n int) {
	defer func() {
		if recover() != nil {
			n = 0
		}
	}()
	var fr meters.Frame
	e.slot.CopyLatest(&fr)
	n = meters.EncodedSize(&fr)
	if n <= len(dst) {
		meters.AppendBinary(dst[:0], &fr)
	}
	return n
}

// SetLogSink installs (or, with nil, removes) the sink for the engine's log
// lines at minLevel and above (LogDebug, LogInfo, LogWarn, LogError). Without
// a sink nothing is formatted. Channel and console names are user content and
// appear only at LogDebug. Ignored when Options.Logger was given.
func (e *Engine) SetLogSink(sink LogSink, minLevel int) {
	defer func() { _ = recover() }()
	e.logs.set(sink, minLevel)
}

// Close stops the session, a running discovery and the log goroutine, and
// waits for them (Go hosts and tests; apps keep one engine for the process).
func (e *Engine) Close() {
	_ = e.Stop()
	e.disc.stop()
	e.logs.set(nil, LogInfo)
}

// FaderPosition maps dB to a fader position 0..1 with the family's law. Never blocks.
func (e *Engine) FaderPosition(db float64) float64 {
	if b := e.law.Load(); b != nil {
		return b.law.PositionFromDB(db)
	}
	return 0
}

// FaderDB maps a fader position 0..1 to dB (-inf at 0) with the family's law. Never blocks.
func (e *Engine) FaderDB(position float64) float64 {
	if b := e.law.Load(); b != nil {
		return b.law.DBFromPosition(position)
	}
	return math.Inf(-1)
}
