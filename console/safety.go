// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"math"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Errors of the phone safety rules (contracts/core-api README v0.3).
var (
	// ErrNeedsConfirm: a typed raise of more than 6 dB without Confirmed (CR-26).
	ErrNeedsConfirm = errors.New("console: typed raise over 6 dB needs confirmation")
	// ErrShowMode: show mode locks this write (raising Gain, CR-24).
	ErrShowMode = errors.New("console: locked in show mode")
	// ErrGainBound: a Gain write more than 6 dB from the console value (CR-23).
	ErrGainBound = errors.New("console: Gain write over the 6 dB bound")
	// ErrTypedOrigin: only a person types; Typed or Confirmed from another
	// origin (a macro) would skip the bound SR-31 puts on macros.
	ErrTypedOrigin = errors.New("console: typed entry needs a gesture origin")
)

// MonitorCeiling makes a client a monitor seat (SR-28, CR-25): its level
// writes stop at the engineer's latest value + HeadroomDB, never below it.
type MonitorCeiling struct {
	HeadroomDB float64 // 0..10
}

// ClientSafety are the safety settings of one client (one device).
type ClientSafety struct {
	SlipGuard bool            // SR-26, per device class
	Monitor   *MonitorCeiling // nil: not a monitor seat
}

// SafetyConfig are the defaults for every client and the session settings.
type SafetyConfig struct {
	Client   ClientSafety // default for clients without SetClientSafety
	ShowMode bool         // SR-27, restored by the UI at Start
}

// Write is one user write with the flags of the safety rules.
type Write struct {
	driver.Command
	Typed     bool // typed on a keypad: no window, no Gain bound; big raises need Confirmed
	Confirmed bool // the user confirmed a big typed raise (CR-26)
	Undo      bool // undoes an earlier entry (show mode undo grace, SR-27)
}

// Result is what the core did with a write or a hold (CR-22).
type Result struct {
	Value   model.Value
	State   GestureState
	Written bool
	Window  *Window // the active window, if any
}

// safety is the supervisor's safety state. Guarded by Supervisor.mu.
type safety struct {
	cfg      SafetyConfig
	clients  map[string]ClientSafety
	windows  map[holdKey]*window
	showMode bool
	unlocks  unlocks
	engineer map[model.ParamRef]float64    // latest value not from a monitor client (SR-28)
	pending  map[model.ParamRef][]inFlight // Gain values in flight (pending.go)
	latest   map[model.ParamRef]lastSend   // latest Gain value sent, no report since (pending.go)
}

func newSafety(cfg SafetyConfig) safety {
	return safety{cfg: cfg, clients: map[string]ClientSafety{}, windows: map[holdKey]*window{}, showMode: cfg.ShowMode,
		unlocks: newUnlocks(), engineer: map[model.ParamRef]float64{}, pending: map[model.ParamRef][]inFlight{}, latest: map[model.ParamRef]lastSend{}}
}

func (s *safety) client(id string) ClientSafety {
	if c, ok := s.clients[id]; ok {
		return c
	}
	return s.cfg.Client
}

// SetClientSafety sets the safety settings of one client (a MixLink device
// from its pairing token; the in-process UI uses the config and SetSlipGuard).
func (s *Supervisor) SetClientSafety(clientID string, c ClientSafety) {
	s.mu.Lock()
	s.safe.clients[clientID] = c
	s.mu.Unlock()
}

// SetSlipGuard turns the slip guard on or off for one client (SR-26). It
// writes nothing and never changes an active window.
func (s *Supervisor) SetSlipGuard(clientID string, on bool) {
	s.mu.Lock()
	c := s.safe.client(clientID)
	c.SlipGuard = on
	s.safe.clients[clientID] = c
	s.mu.Unlock()
}

// isLevel reports whether f is a level a gesture window applies to.
func isLevel(f model.Field) bool { return f == model.FieldFader || f == model.FieldGain }

func dbOf(v model.Value) float64 {
	db, ok := v.DB()
	if !ok {
		return math.Inf(-1)
	}
	return db
}

// isMonitorOrigin: a write of a monitor client of this core.
func (s *safety) isMonitorOrigin(o driver.Origin) bool {
	return o.MayWrite() && s.client(o.ClientID).Monitor != nil
}

// noteChange keeps the engineer's latest value (SR-28): every level value
// except the writes of a monitor client.
func (s *safety) noteChange(c Change) {
	if c.Param.Field == model.FieldFader && !s.isMonitorOrigin(c.Origin) {
		s.engineer[c.Param] = dbOf(c.Value)
	}
}

// adoptState takes the values of a (re)connect: a value that changed
// meanwhile has an unknown origin and counts as the engineer's.
func (s *safety) adoptState(prev, next model.State) {
	for _, st := range next.Strips() {
		p := model.ParamRef{Strip: st.Ref, Field: model.FieldFader}
		old, _ := prev.Value(p)
		if _, seen := s.engineer[p]; !seen || old.Kind() == 0 || dbOf(old) != st.FaderDB {
			s.engineer[p] = st.FaderDB
		}
	}
}

// ceiling of a monitor client's write to p: engineer's latest + headroom,
// never below the latest (SR-28). ok is false when there is none.
func (s *safety) ceiling(p model.ParamRef, client string, cur float64) (float64, bool) {
	m := s.client(client).Monitor
	if m == nil || p.Field != model.FieldFader {
		return 0, false
	}
	ref, ok := s.engineer[p]
	if !ok {
		ref = cur
	}
	return ref + m.HeadroomDB, true
}

// openWindow makes the window of a new hold on a level parameter.
func (s *Supervisor) openWindowLocked(k holdKey) *window {
	cur, _ := s.state.Value(k.param)
	start := dbOf(cur)
	w := &window{Window: Window{Start: start}}
	switch k.param.Field {
	case model.FieldGain:
		w.HasCap, w.CapUp, w.CapDown = true, start+gainCapUpDB, start-gainCapDnDB
		if h, ok := s.caps.Headamp(k.param.Strip.Index); ok { // this headamp's own law (CR-23)
			// The writable top (re-review LOW D), but never below the start: a
			// grab above it never writes down (review pass 3, LOW 3).
			w.CapUp = math.Max(math.Min(w.CapUp, h.WriteMaxDB), start)
			w.CapDown = math.Max(w.CapDown, h.MinDB)
		}
	case model.FieldFader:
		w.law = s.cfg.Driver.FaderLaw()
		if s.safe.client(k.client).SlipGuard {
			w.HasWall, w.WallUp, w.OpensByRest = true, slipWall(start), true
		}
		w.Ceiling, w.HasCeiling = s.safe.ceiling(k.param, k.client, start)
		w.Ceiling = math.Max(w.Ceiling, start) // never lower a value to meet the ceiling (review MEDIUM 3)
	}
	s.safe.windows[k] = w
	return w
}

// clampLocked applies the window, the monitor ceiling, show mode and the
// Gain bound to one write. It returns the value to write, or an error that
// refuses it. Callers hold mu.
func (s *Supervisor) clampLocked(w Write, cur float64) (float64, GestureState, *Window, error) {
	p, c := w.Param, dbOf(w.Value)
	if w.Typed && c-cur > bigRaiseDB && !w.Confirmed {
		return 0, 0, nil, fmt.Errorf("%w: %s %.1f to %.1f dB", ErrNeedsConfirm, p, cur, c)
	}
	v, st := c, StateFree
	win := s.safe.windows[holdKey{param: p, client: w.Origin.ClientID}]
	if win != nil && !w.Typed {
		if p.Field == model.FieldGain {
			v, st = win.clampGain(c)
		} else {
			v, st = win.clampFader(c)
		}
	} else if ceil, ok := s.safe.ceiling(p, w.Origin.ClientID, cur); ok && v > math.Max(ceil, cur) {
		v, st = math.Max(ceil, cur), StateAtCeiling // never lower a value to meet the ceiling
	}
	if p.Field != model.FieldGain {
		return v, st, windowOf(win), nil
	}
	return s.gainRulesLocked(w, cur, v, st, win)
}

// gainRulesLocked applies show mode (CR-24) and the 6 dB bound (CR-23).
func (s *Supervisor) gainRulesLocked(w Write, cur, v float64, st GestureState, win *window) (float64, GestureState, *Window, error) {
	h := w.Param.Strip.Index
	s.safe.unlocks.touch(h)
	inFlight := s.safe.inFlightOf(w.Param)
	floor := raiseFloor(cur, inFlight) // a raise above the console value or any value in flight is a raise
	tol := s.halfStepDB(h)
	if s.safe.showMode && v > floor && !s.safe.unlocks.allowsRaise(h, w.Undo, v, tol) {
		if win == nil {
			return 0, 0, nil, fmt.Errorf("%w: raising %s", ErrShowMode, w.Param)
		}
		v, st = floor, StateLocked
	}
	if lo, hi := gainBounds(cur, inFlight); !w.Typed && (v < lo || v > hi) {
		if win == nil {
			return 0, 0, nil, fmt.Errorf("%w: %s %.1f to %.1f dB (console %.1f, in flight %v)", ErrGainBound, w.Param, cur, v, cur, inFlight)
		}
		v, st = math.Min(math.Max(v, lo), math.Max(lo, hi)), StateAtCap
	}
	s.safe.unlocks.noteEntry(h, entryBase(cur, inFlight, win), tol) // any accepted Gain write during an unlock is an entry (undo grace)
	return v, st, windowOf(win), nil
}

// halfStepDB is half a step of headamp h's own law: two Gain values closer
// than that are the same console value (re-review LOW F). 0 (exact) when
// the capabilities do not describe h.
func (s *Supervisor) halfStepDB(h int) float64 {
	info, _ := s.caps.Headamp(h)
	return info.StepDB / 2
}

// entryBase is the Gain an undo of this entry returns to: the start of the
// hold, else the latest value in flight, else the console value.
func entryBase(cur float64, inFlight []float64, win *window) float64 {
	switch {
	case win != nil:
		return win.Start
	case len(inFlight) > 0:
		return inFlight[len(inFlight)-1]
	}
	return cur
}

func windowOf(w *window) *Window {
	if w == nil {
		return nil
	}
	c := w.Window
	return &c
}
