// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
)

// wingLikeUpdate: two inputs, mute groups with unknown membership, group 2
// engaged; input 2 muted by its own mute.
func wingLikeUpdate(t *testing.T) console.Update {
	t.Helper()
	ins := []model.Strip{{Ref: model.StripRef{Kind: model.StripInput, Index: 1}}, {Ref: model.StripRef{Kind: model.StripInput, Index: 2}, Muted: true}}
	st, err := model.NewState(ins, nil)
	if err != nil {
		t.Fatal(err)
	}
	st = st.WithGroups(4, 0).WithMuteRules(model.MuteRules{MuteGroupMembershipUnknown: true})
	if st, err = st.With(model.ParamRef{Strip: model.StripRef{Kind: model.StripMuteGroup, Index: 2}, Field: model.FieldMute}, model.Bool(true)); err != nil {
		t.Fatal(err)
	}
	return console.Update{Status: console.StatusLive, State: st,
		Capabilities: model.Capabilities{InputCount: 2, MuteGroups: 4, MuteGroupMembershipUnknown: true}}
}

// TestSnapshot_UnknownMuteIsNull: review H1; never "muted": false for a
// strip an engaged mute group may silence.
func TestSnapshot_UnknownMuteIsNull(t *testing.T) {
	b, err := encodeUpdate(wingLikeUpdate(t), 1, true, false)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{
		`"muteGroupMembership":false`,
		`"effectiveMute":{"muted":null,"basis":"partial","own":false`,
		`"effectiveMute":{"muted":true,"basis":"derived","own":true`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("snapshot lacks %s:\n%s", want, js)
		}
	}
	if strings.Contains(js, `"mute":false,"muteGroups":`) || strings.Contains(js, `"mute":true,"muteGroups":`) {
		t.Errorf("a strip must not claim an empty mute-group membership it does not know:\n%s", js)
	}
}

// TestDiff_InternalFieldNeverListed: the console's own mute report is an
// internal field; clients only see its effect on effectiveMute.
func TestDiff_InternalFieldNeverListed(t *testing.T) {
	u := wingLikeUpdate(t)
	v, _ := model.Index(model.ConsoleMuteOn)
	u.Changes = []console.Change{{Param: model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldConsoleMute},
		Value: v, Origin: driver.Origin{Kind: driver.OriginConsole}}}
	b, err := encodeUpdate(u, 2, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "consoleMute") {
		t.Fatalf("internal field in a diff: %s", b)
	}
}

// mutedTrue reads effectiveMute.muted; null (unknown) is not true.
func mutedTrue(e *effectiveMuteJSON) bool { return e != nil && e.Muted != nil && *e.Muted }

// TestState_ReasonCodeProtocolMismatch (go-reviewer L1): a mismatch is a
// stable code in every state message, not only diagnostic text.
func TestState_ReasonCodeProtocolMismatch(t *testing.T) {
	u := wingLikeUpdate(t)
	u.Status, u.Reason = console.StatusConnecting, "diagnostic"
	u.Cause = fmt.Errorf("wing: connect: %w", driver.ErrProtocolMismatch)
	for _, full := range []bool{true, false} {
		b, err := encodeUpdate(u, 3, full, false)
		if err != nil || !strings.Contains(string(b), `"reasonCode":"ERR_PROTOCOL_MISMATCH"`) {
			t.Fatalf("full %v: %s %v", full, b, err)
		}
	}
	u.Cause = errors.New("connection refused")
	if b, _ := encodeUpdate(u, 4, true, false); strings.Contains(string(b), "reasonCode") {
		t.Fatalf("another cause has no code: %s", b)
	}
}
