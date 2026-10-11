// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package safe

import (
	"errors"
	"sync"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestGo_RecoversPanicIntoCallback(t *testing.T) {
	var wg sync.WaitGroup
	var got error
	Go(&wg, func(err error) { got = err }, func() { panic("boom") })
	wg.Wait()
	if !errors.Is(got, ErrPanic) {
		t.Fatalf("callback got %v, want an error wrapping ErrPanic", got)
	}
}

func TestGo_NilCallbackStillRecovers(t *testing.T) {
	var wg sync.WaitGroup
	Go(&wg, nil, func() { panic("boom") })
	wg.Wait() // reaching this line means the process did not crash
}

func TestRecoverAsError(t *testing.T) {
	f := func() (err error) {
		defer RecoverAsError(&err)
		panic("boom")
	}
	if err := f(); !errors.Is(err, ErrPanic) {
		t.Fatalf("got %v, want ErrPanic", err)
	}
	g := func() (err error) {
		defer RecoverAsError(&err)
		return nil
	}
	if err := g(); err != nil {
		t.Fatalf("no panic must leave err nil, got %v", err)
	}
}
