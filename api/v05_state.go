// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"fmt"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/model"
)

// State JSON of core-api v0.5 (state.schema.json): showModeLocked and undo
// in every message, favourites in snapshots and after a change, and the
// externalChangeInScope event.

const eventScope = "externalChangeInScope"

// v05StateJSON is embedded in stateJSON (its fields are flattened).
type v05StateJSON struct {
	ShowModeLocked bool         `json:"showModeLocked,omitempty"`
	Favourites     *[]stripJSON `json:"favourites,omitempty"`
	Undo           []undoJSON   `json:"undo,omitempty"`
}

// undoJSON is state.schema.json $defs/undoEntry.
type undoJSON struct {
	ClientID  string         `json:"clientId"`
	Param     paramJSON      `json:"param"`
	Kind      string         `json:"kind"`
	From      any            `json:"from"`
	To        any            `json:"to"`
	Delta     *float64       `json:"delta,omitempty"`
	Direction string         `json:"direction"`
	Asks      []string       `json:"asks"`
	Scene     *sceneMarkJSON `json:"scene,omitempty"`
}

type sceneMarkJSON struct {
	Number int    `json:"number,omitempty"`
	Name   string `json:"name,omitempty"`
}

// v05StateOf renders the v0.5 fields of an update; favourites only when
// withFavourites (a snapshot, or the first message after a change).
func v05StateOf(u console.Update, withFavourites bool) v05StateJSON {
	out := v05StateJSON{ShowModeLocked: u.ShowModeLocked}
	if withFavourites {
		fav := favouritesJSON(u.Favourites)
		out.Favourites = &fav
	}
	for _, s := range u.Undo {
		j := undoJSON{ClientID: s.ClientID, Param: paramToJSON(s.Param), Kind: s.Kind.String(), From: valueJSON(s.From),
			To: valueJSON(s.To), Direction: s.Direction, Asks: append([]string{}, s.Asks...)}
		if s.HasDelta {
			d := s.Delta
			j.Delta = &d
		}
		if s.Scene != nil {
			j.Scene = &sceneMarkJSON{Number: s.Scene.Number, Name: s.Scene.Name}
		}
		out.Undo = append(out.Undo, j)
	}
	return out
}

func favouritesJSON(list []model.StripRef) []stripJSON {
	out := make([]stripJSON, 0, len(list))
	for _, r := range list {
		out = append(out, stripJSON{Kind: r.Kind.String(), Index: r.Index})
	}
	return out
}

// scopeEventsJSON renders watch events (CC-11).
func scopeEventsJSON(evs []console.ScopeEvent) []eventJSON {
	out := make([]eventJSON, 0, len(evs))
	for _, e := range evs {
		client := e.ClientID
		out = append(out, eventJSON{Type: eventScope, ClientID: &client, Scope: &busScopeJSON{Bus: e.Bus}, Count: e.Count})
	}
	return out
}

// v05Key is what the dispatcher compares to send a message when only the
// v0.5 state moved (an undo ask ran out, the PIN lock, the favourites).
func v05Key(u console.Update) string {
	return fmt.Sprint(u.ShowModeLocked, u.Favourites, u.Undo)
}

// favouritesKey tells the dispatcher when the favourites changed.
func favouritesKey(u console.Update) string { return fmt.Sprint(u.Favourites) }
