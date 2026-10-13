// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"slices"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Favourites (redesign CC-7, D-23, core-api v0.5 rule 17): one ordered
// user layer of strips for this console on this device. It lives with the
// engine, is restored from the config and is never written to the console.

// MaxFavourites bounds the favourites layer (fader-and-gain.md 15, CC-7).
const MaxFavourites = 32

// ErrFavourites: a favourites list the contract refuses.
var ErrFavourites = errors.New("console: invalid favourites")

// ValidFavourite reports whether a strip may be a favourite: an input, the
// main, a bus, a DCA or a matrix, with a valid index.
func ValidFavourite(r model.StripRef) error {
	switch r.Kind {
	case model.StripMain:
		if r.Index == 0 {
			return nil
		}
	case model.StripInput, model.StripBus, model.StripDCA, model.StripMatrix:
		if r.Index >= 1 {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrFavourites, r)
}

// validFavourites checks a whole layer: valid strips, each once, at most 32.
func validFavourites(list []model.StripRef) error {
	if len(list) > MaxFavourites {
		return fmt.Errorf("%w: %d strips, at most %d", ErrFavourites, len(list), MaxFavourites)
	}
	for i, r := range list {
		if err := ValidFavourite(r); err != nil {
			return err
		}
		if slices.Contains(list[:i], r) {
			return fmt.Errorf("%w: %s twice", ErrFavourites, r)
		}
	}
	return nil
}

// SetFavourites replaces the favourites layer. Any role may set it (it
// changes only this device); it writes nothing.
func (s *Supervisor) SetFavourites(origin driver.Origin, list []model.StripRef) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if err := validFavourites(list); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.favourites = slices.Clone(list)
	s.signal()
	return nil
}
