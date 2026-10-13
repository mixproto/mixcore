// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package burst

import (
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestWindow_DistinctWithinSpan(t *testing.T) {
	w := New(250 * time.Millisecond)
	t0 := time.Unix(0, 0)
	for i := range 7 {
		if n := w.Add(i, t0.Add(time.Duration(i)*10*time.Millisecond)); n != i+1 {
			t.Fatalf("push %d: %d distinct", i, n)
		}
	}
	if n := w.Add(0, t0.Add(100*time.Millisecond)); n != 7 {
		t.Fatalf("a repeat is not a new parameter: %d", n)
	}
	if n := w.Add(9, t0.Add(330*time.Millisecond)); n != 2 {
		t.Fatalf("after the span only 0 (pushed at 100 ms) and 9 remain: %d", n)
	}
}

func TestWindow_BoundedByDistinct(t *testing.T) {
	w := New(time.Second)
	t0 := time.Unix(0, 0)
	for i := range 100000 {
		w.Add(i%5, t0.Add(time.Duration(i)*time.Microsecond))
	}
	if w.Len() > 2*5+16 {
		t.Fatalf("queue of %d for 5 parameters", w.Len())
	}
	w.Reset()
	if w.Add(1, t0) != 1 {
		t.Fatal("reset")
	}
}

func FuzzWindow(f *testing.F) {
	f.Add([]byte{1, 2, 3, 1, 200, 5})
	f.Fuzz(func(t *testing.T, b []byte) {
		w := New(250 * time.Millisecond)
		at := time.Unix(0, 0)
		for i := 0; i+1 < len(b); i += 2 {
			at = at.Add(time.Duration(b[i+1]) * time.Millisecond)
			if n := w.Add(int(b[i]%16), at); n < 1 || n > 16 || w.Len() > 2*16+16+1 {
				t.Fatalf("distinct %d, queue %d", n, w.Len())
			}
		}
	})
}
