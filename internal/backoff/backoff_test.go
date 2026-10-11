// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backoff

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestDelay(t *testing.T) {
	p := Policy{Initial: 100 * time.Millisecond, Max: 2 * time.Second, Multiplier: 2, Jitter: 0.2}
	tests := []struct {
		name    string
		attempt int
		rand01  float64
		want    time.Duration
	}{
		{"first attempt, no jitter offset", 1, 0.5, 100 * time.Millisecond},
		{"first attempt, lowest jitter", 1, 0, 80 * time.Millisecond},
		{"first attempt, highest jitter", 1, 0.999999, 120 * time.Millisecond},
		{"third attempt doubles twice", 3, 0.5, 400 * time.Millisecond},
		{"capped at Max", 10, 0.5, 2 * time.Second},
		{"jitter never exceeds Max", 10, 0.999999, 2 * time.Second},
		{"jitter below Max at the cap", 10, 0, 1600 * time.Millisecond},
		{"attempt 0 treated as 1", 0, 0.5, 100 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := p.Delay(tc.attempt, tc.rand01)
			if diff := got - tc.want; diff > time.Microsecond || diff < -time.Microsecond {
				t.Fatalf("Delay(%d, %v) = %v, want %v", tc.attempt, tc.rand01, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	if err := Default.Validate(); err != nil {
		t.Fatalf("Default policy invalid: %v", err)
	}
	bad := []Policy{
		{Initial: 0, Max: time.Second, Multiplier: 2},
		{Initial: time.Second, Max: time.Millisecond, Multiplier: 2},
		{Initial: time.Millisecond, Max: time.Second, Multiplier: 0.5},
		{Initial: time.Millisecond, Max: time.Second, Multiplier: 2, Jitter: 1.5},
	}
	for i, p := range bad {
		if err := p.Validate(); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("case %d: Validate() = %v, want ErrInvalidPolicy", i, err)
		}
	}
}
