// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// Favourites (redesign CC-7, D-23, core-api v0.5 rule 17): an ordered user
// layer of strips per client (per device, review sec L4). The config's
// layer is the default of every client that has not set its own. It is
// never written to the console.

const (
	// MaxFavourites bounds one favourites layer (fader-and-gain.md 15, CC-7).
	MaxFavourites = 32
	// maxFavouriteIndex bounds a favourite strip's index (no family has more
	// strips of a kind; common.schema.json favourites).
	maxFavouriteIndex = 128
	// maxFavouriteClients bounds the clients with their own layer; a new
	// one beyond it replaces the layer set longest ago.
	maxFavouriteClients = 64
)

// FavouritesLayer is one client's favourites; ClientID "" is the default.
type FavouritesLayer struct {
	ClientID string
	Strips   []model.StripRef
}

type clientFavourites struct {
	strips []model.StripRef
	at     time.Time
}

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
		if r.Index >= 1 && r.Index <= maxFavouriteIndex {
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

// SetFavourites replaces the favourites layer of origin's client. Any role
// may set it (it changes only that device); it writes nothing.
func (s *Supervisor) SetFavourites(origin driver.Origin, list []model.StripRef) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	if err := validFavourites(list); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, known := s.clientFavs[origin.ClientID]; !known && len(s.clientFavs) >= maxFavouriteClients {
		oldest := ""
		for c, f := range s.clientFavs {
			if oldest == "" || f.at.Before(s.clientFavs[oldest].at) {
				oldest = c
			}
		}
		delete(s.clientFavs, oldest)
	}
	s.clientFavs[origin.ClientID] = clientFavourites{strips: slices.Clone(list), at: time.Now()}
	s.signal()
	return nil
}

// favouritesLocked lists the layers: the default first (when it has
// strips), then every client's own, by client id. Callers hold mu.
func (s *Supervisor) favouritesLocked() []FavouritesLayer {
	var out []FavouritesLayer
	if len(s.favourites) > 0 {
		out = append(out, FavouritesLayer{Strips: s.favourites})
	}
	for _, c := range slices.Sorted(maps.Keys(s.clientFavs)) {
		out = append(out, FavouritesLayer{ClientID: c, Strips: s.clientFavs[c].strips})
	}
	return out
}
