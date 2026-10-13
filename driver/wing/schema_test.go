// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"testing"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
)

// minimalDefs is a schema of one channel with the given leaf types.
func minimalDefs(fdr, mute wingproto.NodeType, tags wingproto.NodeType) map[string]wingproto.Def {
	h := uint32(1)
	d := func(t wingproto.NodeType) wingproto.Def { h++; return wingproto.Def{Hash: h, Type: t} }
	return map[string]wingproto.Def{
		"/ch": d(wingproto.TypeNode), "/ch/1": d(wingproto.TypeNode),
		"/ch/1/fdr": d(fdr), "/ch/1/mute": d(mute), "/ch/1/name": d(wingproto.TypeString),
		"/ch/1/$name": d(wingproto.TypeString), "/ch/1/tags": d(tags), "/cfg/dcamgrp": d(wingproto.TypeString),
	}
}

// TestBuildTable_ExpectedLeafTypes (review H2): a fader is a fader and a
// mute an int; a required leaf of another type fails the schema, an
// optional one is absent.
func TestBuildTable_ExpectedLeafTypes(t *testing.T) {
	if _, err := buildTable(minimalDefs(wingproto.TypeInt, wingproto.TypeInt, wingproto.TypeString)); !errors.Is(err, errSchema) {
		t.Fatalf("an int fader: %v", err)
	}
	if _, err := buildTable(minimalDefs(wingproto.TypeFader, wingproto.TypeString, wingproto.TypeString)); !errors.Is(err, errSchema) {
		t.Fatalf("a string mute: %v", err)
	}
	tab, err := buildTable(minimalDefs(wingproto.TypeFader, wingproto.TypeInt, wingproto.TypeInt))
	if err != nil {
		t.Fatal(err)
	}
	if tab.tags[0] != -1 {
		t.Fatal("int tags must be absent")
	}
	for _, p := range tab.all {
		if p.conv == convDCAMgrp {
			t.Fatal("a string /cfg/dcamgrp must be absent")
		}
	}
	if _, ok := tab.byRef[model.ParamRef{Strip: model.StripRef{Kind: model.StripInput, Index: 1}, Field: model.FieldFader}]; !ok {
		t.Fatal("fader missing")
	}
}

// TestAttribute_ParentAndName (review H2): outside the root level a
// definition belongs only to the node its parent names; names are one
// path segment.
func TestAttribute_ParentAndName(t *testing.T) {
	s := &session{}
	nodes := []walkNode{{path: "/ch", hash: 7}, {path: "/dca", hash: 8}}
	byParent := map[uint32]int{7: 0, 8: 1}
	cases := []struct {
		def  wingproto.Def
		want bool
	}{
		{wingproto.Def{Parent: 8, Name: "1"}, true},
		{wingproto.Def{Parent: 9, Name: "1"}, false},
		{wingproto.Def{Parent: 7, Name: "a/b"}, false},
		{wingproto.Def{Parent: 7, Name: ""}, false},
		{wingproto.Def{Parent: 7, Name: "\x00x"}, false},
	}
	for _, tc := range cases {
		if _, ok := s.attribute(nodes, byParent, inMsg{kind: msgDef, def: tc.def}); ok != tc.want {
			t.Errorf("%+v: %v", tc.def, ok)
		}
	}
	if _, ok := s.attribute([]walkNode{{root: true}}, nil, inMsg{kind: msgDef, def: wingproto.Def{Parent: 12345, Name: "ch"}}); !ok {
		t.Fatal("the root level takes any parent")
	}
}
