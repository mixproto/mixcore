// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package driver is the contract every console family implements. It does no
// I/O. Drivers never reconnect on their own: console.Supervisor owns the
// lifecycle, so reconnect, stale marking and write dropping are written once.
package driver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/mixproto/mixcore/meters"
	"github.com/mixproto/mixcore/model"
)

// Family names a console protocol family, e.g. "x32".
type Family string

// OriginKind says who caused a change or a write.
type OriginKind uint8

// Origin kinds. Only Gesture and Macro may produce a parameter write.
const (
	OriginGesture OriginKind = iota + 1 // a person touched a control
	OriginMacro                         // a macro the user built and triggered
	OriginConsole                       // reported by the console: surface, other client, scene recall
	OriginSystem                        // our housekeeping (renewals, reads); never a write
)

var originNames = map[OriginKind]string{
	OriginGesture: "gesture", OriginMacro: "macro", OriginConsole: "console", OriginSystem: "system",
}

func (k OriginKind) String() string {
	if s, ok := originNames[k]; ok {
		return s
	}
	return "unknown"
}

// ParseOriginKind parses the String form of an origin kind.
func ParseOriginKind(s string) (OriginKind, bool) {
	for k, name := range originNames {
		if name == s {
			return k, true
		}
	}
	return 0, false
}

// Origin is attached to every command and every change.
type Origin struct {
	Kind     OriginKind
	ClientID string // client of the gesture; empty for console/system
	Seq      uint64 // client gesture sequence, used for echo reconciliation and the write audit
}

// MayWrite reports whether this origin is allowed to change the console.
func (o Origin) MayWrite() bool { return o.Kind == OriginGesture || o.Kind == OriginMacro }

// Command is one parameter write requested by a user.
type Command struct {
	Param  model.ParamRef
	Value  model.Value // engineering units, validated by model
	Origin Origin
}

// Endpoint is a console address. Port 0 means the family's default port.
type Endpoint struct {
	Host string
	Port int
}

// ListenPacketFunc opens an unconnected packet socket. Apps override it to
// bind sockets to a network (Android) and tests to use an in-memory network.
type ListenPacketFunc func(ctx context.Context, network, address string) (net.PacketConn, error)

// WriteRecord is one parameter write a driver put on the wire. Tests join these
// with the simulator's write log: every console-side write must match one record.
type WriteRecord struct {
	Param   model.ParamRef
	Address string // protocol address of the write
	Origin  Origin
	At      time.Time
}

// Options are given to Connect.
type Options struct {
	Logger       *slog.Logger     // nil: discard
	Meters       *meters.Slot     // where meter frames go; nil: meters are dropped
	ListenPacket ListenPacketFunc // nil: the operating system's UDP sockets
	WriteAudit   func(WriteRecord)
}

// Identity is what the console reported about itself.
type Identity struct {
	Address  string
	Name     string
	Model    string
	Firmware string
}

// MeterRequest selects meter streams. The zero value turns meters off.
type MeterRequest struct {
	Inputs bool
}

// FaderLaw maps a fader position 0..1 to dB and back for one family, so that
// every UI draws the same scale. Implementations are pure and never block.
type FaderLaw interface {
	PositionFromDB(db float64) float64  // -inf and anything below the bottom give 0
	DBFromPosition(pos float64) float64 // 0 gives -inf; positions are clamped to 0..1
}

// Driver creates sessions for one protocol family. Stateless and safe for concurrent use.
type Driver interface {
	Family() Family
	FaderLaw() FaderLaw
	// Connect dials, identifies the console, subscribes and reads the full state.
	// It sends reads, subscriptions and keep-alives only, never a parameter write.
	Connect(ctx context.Context, ep Endpoint, opts Options) (Session, error)
}

// LinkOpener is implemented by drivers whose sessions can share one
// long-lived transport. Some consoles tell clients apart by source port and
// keep a client slot for a while after its last packet (x32: X32-F28), so a
// new socket per reconnect can fill the console's client table. A supervisor
// opens one Link per engine session and reuses it for every reconnect.
type LinkOpener interface {
	OpenLink(ctx context.Context, ep Endpoint, opts Options) (Link, error)
}

// Link owns the sockets to one console. Sessions made by Connect run over
// them one at a time; closing a session never closes the link.
type Link interface {
	// Connect identifies the console, subscribes and reads the full state,
	// like Driver.Connect, over the link's sockets. A session still open on
	// the link is closed first. It never writes a parameter.
	Connect(ctx context.Context) (Session, error)
	// Err is non-nil once a socket of the link failed; the link must then be
	// closed and a new one opened.
	Err() error
	// Close closes the open session, if any, and the sockets. Idempotent.
	Close() error
}

// Session is one live connection, owned by exactly one console.Supervisor.
type Session interface {
	Identity() Identity
	Capabilities() model.Capabilities
	Snapshot() model.State // state read during Connect

	// Run owns the connection until ctx is cancelled (returns nil) or the console
	// is lost (returns an error wrapping ErrConnectionLost). It renews
	// subscriptions, detects a dead console by last-received time and emits
	// every change on events.
	Run(ctx context.Context, events chan<- Event) error

	// Apply hands one write to the session and returns at once.
	// Errors: ErrNotUserOrigin, ErrUnsupported, ErrClosed, model.ErrInvalidValue.
	Apply(cmd Command) error

	// SetMeters replaces the meter subscription (zero request = meters off).
	SetMeters(req MeterRequest) error

	Close() error // idempotent; safe before, during and after Run
}

// Rereader is implemented by sessions that can re-read their state in the
// background, to recover changes whose push was lost (UDP drops silently).
// Reread asks for one paced, read-only pass and returns at once; a pass
// already running is not restarted. Only values that differ from what the
// session last saw are emitted, as ParamChanged with a console origin. A
// parameter written recently by us is not re-read, and a reply that crosses
// one of our writes is discarded, so the pass can never mistake an old value
// for a desk change. It never writes. Errors: ErrClosed.
type Rereader interface {
	Reread() error
}

// Event is emitted by Session.Run.
type Event interface{ isEvent() }

// ParamChanged is a console-side change, or the echo of one of our writes (then
// Origin is the origin of that write).
type ParamChanged struct {
	Param  model.ParamRef
	Value  model.Value
	Origin Origin
	At     time.Time
}

// StateReplaced is a full re-read (scene recall or similar).
type StateReplaced struct {
	State  model.State
	Reason string
}

// Reasons of ClientLimitHit.
const (
	LimitNoEcho          = "noEchoOfOwnWrites"          // our writes got no echo while the console answered
	LimitUnpushedChanges = "rereadFoundUnpushedChanges" // re-reads found changes that were never pushed
)

// ClientLimitHit means the console accepts us but does not push updates to
// us: its client slots are full. Values may be stale until ClientLimitCleared.
type ClientLimitHit struct {
	Limit  int
	Reason string // LimitNoEcho or LimitUnpushedChanges
}

// ClientLimitCleared means updates arrive again after a ClientLimitHit.
type ClientLimitCleared struct{}

// Scene recall confidences and signals (SceneRecalled).
const (
	ConfidenceHigh   = "high"   // a burst and the family's recall marker
	ConfidenceMedium = "medium" // the recall marker alone
	ConfidenceLow    = "low"    // a burst alone

	SignalBurst            = "burst"            // many parameters pushed within a short window
	SignalUndoTime         = "undoTime"         // x32: the console's undo checkpoint changed
	SignalHighlightedScene = "highlightedScene" // x32: the highlighted scene changed (a hint, never a signal alone)
	SignalSnapshotIndex    = "snapshotIndex"    // xair: the console pushed its loaded snapshot number

	NoScene = -1 // HighlightedScene or LoadedScene when the console did not report one
)

// SceneRecalled means a scene (snapshot) was recalled at the console, as far
// as the driver can tell; the family's protocol doc says how it is inferred.
// Every held parameter must end; the driver re-reads the console itself.
type SceneRecalled struct {
	Confidence       string
	Signals          []string
	HighlightedScene int // NoScene when unknown; never "the loaded scene"
	LoadedScene      int // NoScene when unknown; set only by families whose marker names the loaded scene (xair)
	At               time.Time
}

func (ParamChanged) isEvent()       {}
func (SceneRecalled) isEvent()      {}
func (StateReplaced) isEvent()      {}
func (ClientLimitHit) isEvent()     {}
func (ClientLimitCleared) isEvent() {}

// Sentinel errors. Compare with errors.Is.
var (
	ErrConnectionLost = errors.New("console connection lost")
	ErrStale          = errors.New("console state is stale; input locked")
	ErrNotUserOrigin  = errors.New("write without a user gesture")
	ErrUnsupported    = errors.New("capability not supported by this console")
	ErrClosed         = errors.New("session closed")
)
