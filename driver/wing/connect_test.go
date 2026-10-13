// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"

	"github.com/mixproto/mixcore/internal/wingproto"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

// TestConnect_ReadsStateAndSendsNoWrites: rule 1, the console is the source
// of truth; connecting reads and never writes.
func TestConnect_ReadsStateAndSendsNoWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{Name: "FOH", Model: "ngc-full", Firmware: "3.0.6-0-gabc:release"})
		for path, v := range map[string]any{
			"/ch/3/fdr": -12.0, "/ch/3/mute": 1, "/ch/3/name": "Vox", "/ch/3/tags": "#D2,#D12,#Drums",
			"/mgrp/2/mute": 1, "/dca/5/mute": 1, "/main/1/fdr": -4.0, "/io/in/LCL/2/g": 20.0, "/io/in/LCL/2/vph": 1,
		} {
			mustSet(t, r.sim, path, v)
		}
		s := r.connect()
		if id := s.Identity(); id.Name != "FOH" || id.Model != "ngc-full" || id.Firmware != "3.0.6-0-gabc:release" || id.Address != consoleIP+":2222" {
			t.Fatalf("identity %+v", id)
		}
		c := s.Capabilities()
		if c.InputCount != 40 || c.DCAs != 16 || c.MuteGroups != 8 || !c.HasMain || !c.EchoesOwnWrites || c.MaxPushClients != 16 || !c.InputHeadamps {
			t.Fatalf("capabilities %+v", c)
		}
		if n := countBank(c, model.BankLocal); n != localInputsFull {
			t.Fatalf("full-size WING has 8 local headamps, got %d (WG-F03)", n)
		}
		st := s.Snapshot()
		ch3, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 3})
		if ch3.FaderDB != -12 || !ch3.Muted || ch3.Name != "Vox" || ch3.DCAs != 1<<1|1<<11 || ch3.Headamp != 3 {
			t.Fatalf("ch 3 %+v", ch3)
		}
		if ch1, _ := st.Strip(model.StripRef{Kind: model.StripInput, Index: 1}); !math.IsInf(ch1.FaderDB, -1) {
			t.Fatalf("-144 dB must read as -inf (WG-F60), got %v", ch1.FaderDB)
		}
		if mg := st.MuteGroups(); !mg[1].Muted || mg[0].Muted {
			t.Fatalf("mute groups %+v", mg)
		}
		if d := st.DCAs(); !d[4].Muted {
			t.Fatalf("dca 5 %+v", d[4])
		}
		ha, _ := st.Strip(model.StripRef{Kind: model.StripHeadamp, Index: 2})
		if ha.GainDB != 20 || !ha.Phantom || !ha.HasPhantom {
			t.Fatalf("headamp 2 %+v", ha)
		}
		if w := r.sim.Writes(); len(w) != 0 {
			t.Fatalf("connect wrote to the console: %+v", w)
		}
	})
}

func countBank(c model.Capabilities, b model.HeadampBank) int {
	n := 0
	for _, h := range c.Headamps {
		if h.Bank == b {
			n++
		}
	}
	return n
}

// TestConnect_LocalHeadampsPerModel: WG-F03 and WG-F62, with the law the
// console defines (2.5 dB) and the document's (0.5 dB).
func TestConnect_LocalHeadampsPerModel(t *testing.T) {
	cases := []struct {
		model     string
		step      float64
		local     int
		wantStep  float64
		wantWrMax float64
	}{
		{"wing-rack", simwing.LocalGainStepCoarse, 24, 2.5, 42.5},
		{"wing-compact", simwing.LocalGainStepFine, 24, 0.5, 45},
		{"ngc-full", simwing.LocalGainStepCoarse, 8, 2.5, 42.5},
	}
	for _, tc := range cases {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, simwing.Options{Model: tc.model, LocalGainStep: tc.step})
			c := r.connect().Capabilities()
			if n := countBank(c, model.BankLocal); n != tc.local {
				t.Fatalf("%s: %d local headamps, want %d", tc.model, n, tc.local)
			}
			h, _ := c.Headamp(1)
			if !h.Writable || h.StepDB != tc.wantStep || math.Abs(h.WriteMaxDB-tc.wantWrMax) > 1e-6 || !h.HasPhantom {
				t.Fatalf("%s: headamp 1 %+v", tc.model, h)
			}
		})
	}
}

// TestConnect_DataEndsWithDE: the walk and the read work whether or not
// the console ends data answers with DE (item 3), also on a reconnect
// whose firmware read precedes a re-walk.
func TestConnect_DataEndsWithDE(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{DataEndsWithDE: true})
		s := r.connect()
		if s.Capabilities().InputCount != 40 {
			t.Fatal("walk failed with DE after data")
		}
	})
}

// TestBuildTable_EmptySchema: a console without the nodes mixcore needs
// (firmware below 2.0 has another definition layout, WG-F05) is a schema error.
func TestBuildTable_EmptySchema(t *testing.T) {
	_, err := buildTable(map[string]wingproto.Def{})
	if !errors.Is(err, errSchema) {
		t.Fatalf("empty schema: %v", err)
	}
}
