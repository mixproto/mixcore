// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

const (
	// maxSourceIndex: a channel's source number is 1..64 (wing.md 3.4).
	maxSourceIndex = 64
	// Local inputs per model (WG-F03): 8 on WING, 24 on Rack and Compact.
	localInputsFull    = 8
	localInputsCompact = 24
	// Client limits (WG-F22, WG-F09): 16 TCP connections, 24 from 3.1.
	clientsBefore31 = 16
	clientsFrom31   = 24
)

// ownerState is everything the owner goroutine keeps between steps.
type ownerState struct {
	known      []model.Value // newest value seen per parameter
	have       []bool
	raw        []wireValue // internal parameters as read during connect
	sent       map[int][]sentWrite
	lastSent   map[int]wireValue // last value written, no settle reply since
	settleAt   map[int]time.Time
	reads      map[int][]readEntry
	srcGroup   []string // per input
	srcIndex   []int32
	headampOf  []int // the headamp each input reports (0 = none)
	dcamgrp    bool
	aesPresent map[int]bool
	lastProbe  time.Time
	lastMeter  time.Time
	meterSince time.Time // when meters were last switched on
	scene      sceneState
	rr         rereadState
	outbox     []driver.Event
	buf        []byte
}

type sentWrite struct {
	val    wireValue
	origin driver.Origin
	at     time.Time
}

// readEntry is one data request in flight. settle: a read-back after our
// write; superseded: we wrote the parameter after asking, so the reply may
// be older than our write and is dropped.
type readEntry struct {
	settle, superseded bool
	at                 time.Time // when it was sent
}

// readExpiry: a read unanswered this long was dropped by the console; its
// entry is forgotten so a later push is not taken for its reply (review
// go L2). Far above a LAN round trip.
const readExpiry = 2 * time.Second

// expireReads forgets reads older than readExpiry.
func (s *session) expireReads(now time.Time) {
	for idx, q := range s.own.reads {
		keep := slices.DeleteFunc(q, func(r readEntry) bool { return now.Sub(r.at) > readExpiry })
		if len(keep) == 0 {
			delete(s.own.reads, idx)
		} else {
			s.own.reads[idx] = keep
		}
	}
}

func newOwnerState(t *table) ownerState {
	n := len(t.all)
	return ownerState{
		known: make([]model.Value, n), have: make([]bool, n), raw: make([]wireValue, n),
		sent: map[int][]sentWrite{}, lastSent: map[int]wireValue{}, settleAt: map[int]time.Time{}, reads: map[int][]readEntry{},
		srcGroup: make([]string, t.inputs), srcIndex: make([]int32, t.inputs),
		headampOf: make([]int, t.inputs), dcamgrp: true, aesPresent: map[int]bool{}, scene: newSceneState(),
	}
}

// remember records the newest value seen for parameter i.
func (s *session) remember(i int, v model.Value) {
	s.own.known[i], s.own.have[i] = v, true
}

// localInputsFor is the number of local preamps of a model string
// (WG-F03, WG-F04): the Rack and the Compact have 24, every other model
// string (ngc-full, wing, wing-bk, an unknown one) the full-size 8, the
// safe side for a string not seen yet.
func localInputsFor(modelName string) int {
	m := strings.ToLower(modelName)
	if strings.Contains(m, "rack") || strings.Contains(m, "compact") {
		return localInputsCompact
	}
	return localInputsFull
}

// clientsFor is the documented TCP connection limit of a firmware string:
// 24 from 3.1, else 16 (WG-F22, WG-F09). Unparseable: 16.
func clientsFor(firmware string) int {
	major, rest, ok := leadingInt(firmware)
	if !ok || !strings.HasPrefix(rest, ".") {
		return clientsBefore31
	}
	minor, _, ok := leadingInt(rest[1:])
	if ok && (major > 3 || (major == 3 && minor >= 1)) {
		return clientsFrom31
	}
	return clientsBefore31
}

// leadingInt reads the decimal digits at the start of s.
func leadingInt(s string) (int, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' && i < 6 {
		i++
	}
	n, err := strconv.Atoi(s[:i])
	return n, s[i:], err == nil
}

// visibleHeadamp is the headamp input n reports: the source's headamp when
// the session has it (not absent), else 0 (wing.md 3.4).
func (s *session) visibleHeadamp(n int) int {
	h := s.tab.headampFor(s.own.srcGroup[n-1], int(s.own.srcIndex[n-1]))
	def, ok := s.tab.headamp(h)
	if !ok || s.absent[def.gain] {
		return 0
	}
	return h
}

// hasInputHeadamps: every input has a readable source group and number.
func (s *session) hasInputHeadamps() bool {
	for n := range s.tab.inputs {
		g, i := s.tab.srcGroup[n], s.tab.srcIndex[n]
		if g < 0 || i < 0 || s.absent[g] || s.absent[i] {
			return false
		}
	}
	return true
}

// capabilities of this console as far as its schema and answers go.
func (s *session) capabilities() model.Capabilities {
	c := model.Capabilities{
		InputCount: s.tab.inputs, AbsentInputs: maxInputs - s.tab.inputs, HasMain: s.tab.hasMain,
		MuteGroups: s.tab.groups, DCAs: s.tab.dcas, InputMeters: true, EchoesOwnWrites: true,
		MaxPushClients: clientsFor(s.ident.Firmware), InputHeadamps: s.hasInputHeadamps(),
		MuteGroupMembershipUnknown: s.tab.groups > 0, // item 11
	}
	for _, h := range s.tab.headamps {
		if s.absent[h.gain] || h.law.step <= 0 {
			continue
		}
		c.Headamps = append(c.Headamps, model.HeadampInfo{
			Index: h.index, Bank: h.bank, Number: h.number, MinDB: h.law.min, MaxDB: h.law.max, StepDB: h.law.step,
			WriteMaxDB: h.law.writeMax(), Writable: h.writable, HasPhantom: h.phantom >= 0 && !s.absent[h.phantom],
		})
	}
	return c
}

// buildState makes the snapshot from the values read.
func (s *session) buildState() (model.State, error) {
	inputs := make([]model.Strip, s.tab.inputs)
	for i := range inputs {
		inputs[i].Ref = model.StripRef{Kind: model.StripInput, Index: i + 1}
	}
	var main *model.Strip
	if s.tab.hasMain {
		main = &model.Strip{Ref: model.MainRef}
	}
	st, err := model.NewState(inputs, main)
	if err != nil {
		return model.State{}, err
	}
	st = st.WithGroups(s.tab.groups, s.tab.dcas).WithMuteRules(s.muteRules())
	if st, err = st.WithHeadamps(s.stateHeadamps()); err != nil {
		return model.State{}, err
	}
	for i, p := range s.tab.all {
		if p.ref == (model.ParamRef{}) || s.absent[i] || !s.own.have[i] || p.ref.Strip.Kind == model.StripHeadamp {
			continue
		}
		if st, err = st.With(p.ref, s.own.known[i]); err != nil {
			return model.State{}, fmt.Errorf("wing: %s: %w", p.path, err)
		}
	}
	if !s.caps.InputHeadamps {
		return st, nil
	}
	for n := 1; n <= s.tab.inputs; n++ {
		v, _ := model.Index(s.own.headampOf[n-1])
		if st, err = st.With(pref(model.StripInput, n, model.FieldHeadamp), v); err != nil {
			return model.State{}, err
		}
	}
	return st, nil
}

// stateHeadamps are the LCL headamps of the model and the AES50 ones a
// channel uses, with the values read.
func (s *session) stateHeadamps() []model.Headamp {
	var out []model.Headamp
	for _, h := range s.tab.headamps {
		if s.absent[h.gain] || !s.own.have[h.gain] || h.law.step <= 0 || (isAES(h.index) && !s.own.aesPresent[h.index]) {
			continue
		}
		ha := model.Headamp{Index: h.index}
		ha.GainDB, _ = s.own.known[h.gain].DB()
		if h.phantom >= 0 && !s.absent[h.phantom] && s.own.have[h.phantom] {
			ha.HasPhantom = true
			ha.Phantom, _ = s.own.known[h.phantom].Bool()
		}
		out = append(out, ha)
	}
	return out
}

// muteRules are what the effective-mute rule cannot know on WING: mute
// group membership (item 11), DCA membership when a channel has no tags,
// and /cfg/dcamgrp (a muted DCA mutes its members only when it is 1).
func (s *session) muteRules() model.MuteRules {
	r := model.MuteRules{MuteGroupMembershipUnknown: s.tab.groups > 0, DCAMuteSilent: !s.own.dcamgrp}
	for _, ti := range s.tab.tags {
		r.DCAMembershipUnknown = r.DCAMembershipUnknown || ti < 0 || s.absent[ti]
	}
	return r
}

// fromWire converts a console value of p to the model (invariant 7).
func (s *session) fromWire(p *param, w wireValue) (model.Value, error) {
	switch p.conv {
	case convFader:
		if w.kind != wireFloat {
			return model.Value{}, fmt.Errorf("fader %+v: %w", w, errBadWire)
		}
		db, err := faderFromWire(w.f)
		if err != nil {
			return model.Value{}, err
		}
		return model.Level(db)
	case convGain:
		if w.kind != wireFloat {
			return model.Value{}, fmt.Errorf("gain %+v: %w", w, errBadWire)
		}
		db, err := p.law.gainFromWire(w.f)
		if err != nil {
			return model.Value{}, err
		}
		return model.Level(db)
	case convMute, convPhantom:
		on, err := switchFromWire(w)
		return model.Bool(on), err
	case convConsoleMute:
		return consoleMuteFromWire(w)
	case convName:
		t, err := textFromWire(w)
		if err != nil {
			return model.Value{}, err
		}
		return model.Text(t)
	case convTags:
		t, err := textFromWire(w)
		return model.Mask(dcaMaskFromTags(t)), err
	}
	return model.Value{}, fmt.Errorf("wing: %s is not a model parameter: %w", p.path, errBadWire)
}

// toWire converts a model value for a write to p (absolute values only).
func toWire(p *param, v model.Value) (wireValue, error) {
	switch p.conv {
	case convFader:
		db, ok := v.DB()
		if !ok {
			return wireValue{}, fmt.Errorf("wing fader needs a level: %w", model.ErrInvalidValue)
		}
		f, err := faderToWire(db)
		return wireValue{kind: wireFloat, f: f}, err
	case convGain:
		db, ok := v.DB()
		if !ok {
			return wireValue{}, fmt.Errorf("wing gain needs a level: %w", model.ErrInvalidValue)
		}
		f, err := p.law.toWire(db)
		return wireValue{kind: wireFloat, f: f}, err
	case convMute:
		on, ok := v.Bool()
		if !ok {
			return wireValue{}, fmt.Errorf("wing mute needs on/off: %w", model.ErrInvalidValue)
		}
		return switchToWire(on), nil
	case convName:
		return nameToWire(p, v)
	}
	return wireValue{}, fmt.Errorf("wing %s: %w", p.path, driver.ErrUnsupported)
}

// nameToWire checks a name against the console's own maximum length
// (wing.md 3.2, WG-F35: the definition's maxlen wins over the document).
func nameToWire(p *param, v model.Value) (wireValue, error) {
	t, ok := v.Text()
	if !ok {
		return wireValue{}, fmt.Errorf("wing name needs text: %w", model.ErrInvalidValue)
	}
	limit := p.maxLen
	if limit <= 0 || limit > math.MaxUint8 {
		limit = math.MaxUint8
	}
	if len(t) > limit {
		return wireValue{}, fmt.Errorf("wing name of %d bytes, the console stores %d: %w", len(t), limit, model.ErrInvalidValue)
	}
	return wireValue{kind: wireText, s: t}, nil
}

// sameWire reports whether two wire values of p are one console value:
// within half a step of the parameter's law (wing.md 2.4), never float
// equality; faders also within faderEchoMinDB (item 10: the console may
// round dB writes to 0.1 dB).
func sameWire(p *param, a, b wireValue) bool {
	switch p.conv {
	case convFader:
		x, errA := faderFromWire(a.f)
		y, errB := faderFromWire(b.f)
		return errA == nil && errB == nil && a.kind == wireFloat && b.kind == wireFloat &&
			(sameFader(x, y) || (x > faderDisplayMinDB && y > faderDisplayMinDB && math.Abs(x-y) <= faderEchoMinDB))
	case convGain:
		return a.kind == wireFloat && b.kind == wireFloat && math.Abs(float64(a.f-b.f)) <= p.law.halfStep()
	}
	return a == b
}

// faderEchoMinDB: half of a 0.1 dB rounding step (hardware item 10 may show
// the console rounds dB writes so); below the hearing threshold anyway.
const faderEchoMinDB = 0.05

// consoleMuteFromWire reads <s>/$mute (wing.md 3.2, WG-F24): 0 not muted,
// 1 muted, 2 a value whose meaning is unverified (item 12): unknown.
func consoleMuteFromWire(w wireValue) (model.Value, error) {
	n, err := intFromWire(w, 0, 2)
	if err != nil {
		return model.Value{}, err
	}
	return model.Index([...]int{model.ConsoleMuteOff, model.ConsoleMuteOn, model.ConsoleMuteUnknown}[n])
}
