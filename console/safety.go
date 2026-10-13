// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"math"
	"time"

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

// ClientSafety are the safety settings of one client (one device). The
// slip guard (SR-26) is retired since core-api v0.5 (redesign CC-1).
type ClientSafety struct {
	Monitor *MonitorCeiling // nil: not a monitor seat
}

// SafetyConfig are the defaults for every client and the session settings.
type SafetyConfig struct {
	Client   ClientSafety // default for clients without SetClientSafety
	ShowMode bool         // SR-27, restored by the UI at Start
	// ShowModeLock is the PIN record of a show mode locked by the owner
	// (showlock.go, CC-12); empty: not locked.
	ShowModeLock string
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
	engineer map[model.ParamRef]float64 // latest value not from a monitor client (SR-28)
	roles    map[string]Role            // per client (roles.go)
	defRole  Role                       // every other client
	// seats are the values monitor clients sent per level parameter
	// (seats.go): reports of them never raise the engineer's value.
	seats map[model.ParamRef]*seatLog
	// window is the driver's echo window (Capabilities.EchoWindow).
	window  time.Duration
	pending map[model.ParamRef][]inFlight // Gain values in flight (pending.go)
	latest  map[model.ParamRef]lastSend   // latest Gain value sent, no report since (pending.go)
}

func newSafety(cfg SafetyConfig, role Role) safety {
	return safety{cfg: cfg, clients: map[string]ClientSafety{}, windows: map[holdKey]*window{}, showMode: cfg.ShowMode,
		unlocks: newUnlocks(), engineer: map[model.ParamRef]float64{}, pending: map[model.ParamRef][]inFlight{}, latest: map[model.ParamRef]lastSend{},
		roles: map[string]Role{}, defRole: role, seats: map[model.ParamRef]*seatLog{}}
}

func (s *safety) client(id string) ClientSafety {
	if c, ok := s.clients[id]; ok {
		return c
	}
	return s.cfg.Client
}

// SetClientSafety sets the safety settings of one client (a MixLink device
// from its pairing token; the in-process UI uses the config).
//
// Unchecked and owner-only (review Sec L5): it checks no role, and a
// client given its own settings could drop its monitor ceiling. Only the
// owner's side of a hub calls it, from the pairing record; a hub must
// never route a device's request to it (see the package doc, "Hub").
func (s *Supervisor) SetClientSafety(clientID string, c ClientSafety) {
	s.mu.Lock()
	s.safe.forgetSeat(clientID, time.Now())
	s.safe.clients[clientID] = c
	s.mu.Unlock()
}

// isLevel reports whether f is a level a gesture window applies to.
func isLevel(f model.Field) bool {
	return f == model.FieldFader || f == model.FieldGain || f == model.FieldSendLevel
}

func dbOf(v model.Value) float64 {
	db, ok := v.DB()
	if !ok {
		return math.Inf(-1)
	}
	return db
}

// isMonitorOrigin: a write of a monitor client of this core (a monitor
// role, or a client with a monitor ceiling).
func (s *safety) isMonitorOrigin(o driver.Origin) bool {
	return o.MayWrite() && (s.client(o.ClientID).Monitor != nil || s.role(o.ClientID).Kind == RoleMonitor)
}

// noteChange keeps the engineer's latest value (SR-28): every fader and
// send level report that is not a monitor client's own echo, by the
// credit rule of seats.go.
func (s *safety) noteChange(c Change, same sameLevel) {
	if !isCeilingField(c.Param.Field) || s.isMonitorOrigin(c.Origin) {
		return
	}
	s.credit(c.Param, dbOf(c.Value), c.OwnWrite, same, time.Now())
}

// adoptState takes the values of a (re)connect, a full read with no hold
// open (stale cancelled them): every value changed meanwhile is credited
// by the rule of seats.go, and an overflowed seat set starts over.
func (s *safety) adoptState(prev, next model.State, same sameLevel) {
	now := time.Now()
	for _, p := range ceilingParams(next) {
		v, _ := next.Value(p)
		old, _ := prev.Value(p)
		if _, seen := s.engineer[p]; !seen || old.Kind() == 0 {
			s.engineer[p] = dbOf(v)
			continue
		}
		if lg := s.seats[p]; lg != nil {
			lg.endOverflow(now, s.echoWindow()) // a full read with no hold open
		}
		if !old.Equal(v) {
			s.credit(p, dbOf(v), false, same, now)
		}
	}
}

// ceilingParams lists every fader and send level of a state (SR-28).
func ceilingParams(st model.State) []model.ParamRef {
	var out []model.ParamRef
	for _, strips := range [][]model.Strip{st.Strips(), st.Buses()} {
		for _, s := range strips {
			out = append(out, model.ParamRef{Strip: s.Ref, Field: model.FieldFader})
		}
	}
	for _, d := range st.DCAs() { // DCA masters (review Go B1)
		p := model.ParamRef{Strip: d.Ref, Field: model.FieldFader}
		if _, ok := st.Value(p); ok {
			out = append(out, p)
		}
	}
	for i := 1; i <= len(st.Inputs()); i++ {
		for _, snd := range st.Sends(i) {
			p := model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: i}, Field: model.FieldSendLevel, Bus: snd.Bus}
			if _, ok := st.Value(p); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// ceiling of a monitor client's write to p: engineer's latest + headroom,
// never below the latest (SR-28); a monitor role's ceiling is the one of
// the bus (CR-25). ok is false when there is none.
func (s *safety) ceiling(p model.ParamRef, client string, cur float64) (float64, bool) {
	m := s.ceilingOf(p, client)
	if m == nil || !isCeilingField(p.Field) {
		return 0, false
	}
	ref, ok := s.engineer[p]
	if !ok {
		ref = ceilingFloorDB // never the current value: a seat may have pushed it up (review Go B1)
	}
	return finiteOr(ref, ceilingFloorDB) + m.HeadroomDB, true
}

// openWindow makes the window of a new hold on a level parameter.
func (s *Supervisor) openWindowLocked(k holdKey) *window {
	cur, _ := s.state.Value(k.param)
	start := dbOf(cur)
	w := &window{Window: Window{Start: start}}
	switch k.param.Field {
	case model.FieldGain:
		// The window is the headamp's range (CC-2); the hold starts where the
		// console is going: the latest value of ours in flight (CC-3).
		if l, ok := s.safe.newestInFlight(k.param); ok {
			start = l
		}
		w.Start, w.lastWrite = start, start
		w.HasCap, w.CapUp, w.CapDown = true, start, start
		if h, ok := s.caps.Headamp(k.param.Strip.Index); ok { // this headamp's own law (CR-23)
			// The writable top (re-review LOW D), but never below the start: a
			// grab above it never writes down (review pass 3, LOW 3).
			w.CapUp = math.Max(h.WriteMaxDB, start)
			w.CapDown = math.Min(h.MinDB, start)
		}
	case model.FieldFader, model.FieldSendLevel:
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
	switch {
	case w.Typed:
	case win != nil: // inside a hold: 6 dB from the hold's previous write (CC-3); no error mid-drag
		v = math.Min(math.Max(v, win.lastWrite-gainBoundDB), win.lastWrite+gainBoundDB)
		if st == StateAtCap && v != win.CapUp && v != win.CapDown {
			st = StateFree // atCap only at a range end (CC-2)
		}
	default:
		if lo, hi := gainBounds(cur, inFlight); v < lo || v > hi {
			return 0, 0, nil, fmt.Errorf("%w: %s %.1f to %.1f dB (console %.1f, in flight %v)", ErrGainBound, w.Param, cur, v, cur, inFlight)
		}
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
