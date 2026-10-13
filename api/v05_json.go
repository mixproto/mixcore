// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"errors"
	"fmt"
	"slices"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// JSON of the core-api v0.5 additions (redesign CC-6 to CC-12): command
// types undo, cancelHold, watch, setFavourites, lockShowMode and
// unlockShowMode, and the state's undo, favourites and showModeLocked.

const (
	cmdUndo       = "undo"           // v0.5, CC-6
	cmdCancelHold = "cancelHold"     // v0.5, CC-9
	cmdWatch      = "watch"          // v0.5, CC-11
	cmdFavourites = "setFavourites"  // v0.5, CC-7
	cmdLockShow   = "lockShowMode"   // v0.5, CC-12
	cmdUnlockShow = "unlockShowMode" // v0.5, CC-12
	maxFavourites = 32               // common.schema.json favourites maxItems
	maxScopeBus   = 64               // common.schema.json busScope bus maximum
)

// busScopeJSON is common.schema.json busScope.
type busScopeJSON struct {
	Bus int `json:"bus"`
}

// v05Fields are the command fields of v0.5, each allowed with one type.
type v05Fields struct {
	Scope      *busScopeJSON `json:"scope"`
	Favourites *[]stripJSON  `json:"favourites"`
	Pin        *string       `json:"pin"`
}

// v05Command is what the v0.5 types carry once parsed.
type v05Command struct {
	scope      int // watch: the bus, 0 = clear
	favourites []model.StripRef
	pin        string
}

// checkV05Fields refuses a v0.5 field on another type, and the flags of
// set on the wrong type (confirmed also goes with undo).
func checkV05Fields(c commandJSON) error {
	switch {
	case (c.Typed != nil || c.Undo != nil) && c.Type != cmdSet:
		return errors.New("typed and undo go with set only")
	case c.Confirmed != nil && c.Type != cmdSet && c.Type != cmdUndo:
		return errors.New("confirmed goes with set and undo only")
	case c.Scope != nil && c.Type != cmdWatch:
		return errors.New("scope goes with watch only")
	case c.Favourites != nil && c.Type != cmdFavourites:
		return errors.New("favourites go with setFavourites only")
	case c.Pin != nil && c.Type != cmdLockShow && c.Type != cmdUnlockShow:
		return errors.New("pin goes with lockShowMode and unlockShowMode only")
	}
	return nil
}

// parseV05 parses the v0.5 types that carry no param.
func parseV05(c commandJSON, cmd command) (command, error) {
	if c.Param != nil || len(c.Value) != 0 {
		return command{}, codeErr(CodeInvalidCommand, fmt.Errorf("%s carries no param and no value", c.Type))
	}
	var err error
	switch c.Type {
	case cmdUndo:
		cmd.confirmed = isTrue(c.Confirmed)
	case cmdWatch:
		if c.Scope != nil {
			if c.Scope.Bus < 1 || c.Scope.Bus > maxScopeBus {
				return command{}, codeErr(CodeInvalidCommand, fmt.Errorf("scope bus %d", c.Scope.Bus))
			}
			cmd.v05.scope = c.Scope.Bus
		}
	case cmdFavourites:
		cmd.v05.favourites, err = parseFavourites(c.Favourites)
	default: // lockShowMode, unlockShowMode
		cmd.v05.pin, err = parsePin(c.Pin)
	}
	if err != nil {
		return command{}, codeErr(CodeInvalidCommand, err)
	}
	if cmd.confirmed && cmd.origin.Kind != driver.OriginGesture {
		return command{}, codeErr(CodeInvalidCommand, errors.New("confirmed needs origin gesture"))
	}
	return cmd, nil
}

// parseFavourites checks a favourites list: kinds input, main, bus, dca and
// matrix, each strip once, at most 32 (CC-7).
func parseFavourites(list *[]stripJSON) ([]model.StripRef, error) {
	if list == nil {
		return nil, errors.New("setFavourites needs favourites")
	}
	if len(*list) > maxFavourites {
		return nil, fmt.Errorf("%d favourites, at most %d", len(*list), maxFavourites)
	}
	out := make([]model.StripRef, 0, len(*list))
	for _, sj := range *list {
		p, err := parseParam(paramJSON{Strip: sj, Field: model.FieldFader.String()})
		if err != nil {
			return nil, err
		}
		if err := console.ValidFavourite(p.Strip); err != nil {
			return nil, err
		}
		if slices.Contains(out, p.Strip) {
			return nil, fmt.Errorf("favourite %s twice", p.Strip)
		}
		out = append(out, p.Strip)
	}
	return out, nil
}

// parsePin checks a show-mode PIN: exactly 4 ASCII digits (CC-12).
func parsePin(pin *string) (string, error) {
	if pin == nil || !console.ValidPIN(*pin) {
		return "", errors.New("pin must be 4 ASCII digits")
	}
	return *pin, nil
}

// maxPinRecord is config.schema.json safety.showModeLock.pinRecord maxLength.
const maxPinRecord = 256

// checkV05 validates the v0.5 config fields: the favourites, and a PIN
// record the core can read (console.New checks the record itself).
func (c configJSON) checkV05() error {
	if c.Favourites != nil {
		if _, err := parseFavourites(c.Favourites); err != nil {
			return err
		}
	}
	if l := c.Safety.ShowModeLock; l != nil && (l.PinRecord == "" || len(l.PinRecord) > maxPinRecord) {
		return errors.New("showModeLock: pinRecord 1..256 characters")
	}
	return nil
}

// favourites is the config's favourites layer (validated by parseConfig).
func (c configJSON) favourites() []model.StripRef {
	if c.Favourites == nil {
		return nil
	}
	out, _ := parseFavourites(c.Favourites)
	return out
}

// showModeLock is the config's PIN record, empty when not locked.
func (c configJSON) showModeLock() string {
	if c.Safety.ShowModeLock == nil {
		return ""
	}
	return c.Safety.ShowModeLock.PinRecord
}

// runV05 dispatches the command types of v0.5; handled is false for others.
func runV05(sup *console.Supervisor, cmd command) (res console.Result, handled bool, err error) {
	switch cmd.kind {
	case cmdUndo:
		res, err = sup.Undo(cmd.origin, cmd.confirmed)
	case cmdCancelHold:
		res, err = sup.CancelHold(cmd.param, cmd.origin)
	case cmdWatch:
		err = sup.Watch(cmd.origin, cmd.v05.scope)
	case cmdFavourites:
		err = sup.SetFavourites(cmd.origin, cmd.v05.favourites)
	case cmdLockShow:
		res.PinRecord, err = sup.LockShowMode(cmd.origin, cmd.v05.pin)
	case cmdUnlockShow:
		err = sup.UnlockShowMode(cmd.origin, cmd.v05.pin)
	default:
		return console.Result{}, false, nil
	}
	return res, true, err
}
