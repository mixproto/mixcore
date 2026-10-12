// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package mobile

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The calls the apps make for "Try the demo mixer", through the binding.
func TestDemo_StartConnectStop(t *testing.T) {
	out, err := StartDemo(`{"version":0,"family":"xair"}`)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Family, Host string
		Port         int
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	w := &stateWaiter{}
	SetStateListener(w)
	defer SetStateListener(nil)
	session, err := Start(fmt.Sprintf(`{"version":0,"family":%q,"host":%q,"port":%d,"meters":{"inputs":true}}`, m.Family, m.Host, m.Port))
	if err != nil || !strings.Contains(session, `"demo":true`) {
		t.Fatalf("Start: %s %v", session, err)
	}
	w.waitLive(t)
	w.mu.Lock()
	last := w.last
	w.mu.Unlock()
	if !strings.Contains(last, `"demo":true`) {
		t.Fatalf("state without demo: %s", last)
	}
	if err := StopDemo(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(); err == nil || err.Error() != ErrNotStarted {
		t.Fatalf("Stop after StopDemo = %v: StopDemo must stop the demo session", err)
	}
}
