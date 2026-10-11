// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build sim

package api

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fdBinder struct {
	mu  sync.Mutex
	fds []int
	err error
}

func (b *fdBinder) BindSocket(fd int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fds = append(b.fds, fd)
	return b.err
}

func (b *fdBinder) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.fds)
}

// Every socket of a session goes through the binder before use (Android).
func TestSimLoopback_SocketBinderSeesEverySocket(t *testing.T) {
	l := newLoopWith(t, Options{})
	b := &fdBinder{}
	l.eng.SetSocketBinder(b)
	if _, err := l.eng.Start(l.cfg); err != nil {
		t.Fatal(err)
	}
	l.waitStatus(t, "live")
	if n := b.count(); n != 2 {
		t.Fatalf("binder saw %d sockets, want 2 (subscription + write)", n)
	}
	for _, fd := range b.fds {
		if fd <= 0 {
			t.Fatalf("fd %d", fd)
		}
	}
}

// A binder that refuses keeps the engine off the network: no session, no write.
func TestSimLoopback_RefusingBinderKeepsTheEngineOffline(t *testing.T) {
	l := newLoopWith(t, Options{})
	l.eng.SetSocketBinder(&fdBinder{err: errors.New("network gone")})
	if _, err := l.eng.Start(l.cfg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if st := l.rec.last(); st.Status == "live" {
		t.Fatal("engine went live although every socket was refused")
	}
	if len(l.sim.Log()) != 0 {
		t.Fatal("a refused socket must not reach the console")
	}
	l.eng.SetSocketBinder(nil)
	l.waitStatus(t, "live")
}
