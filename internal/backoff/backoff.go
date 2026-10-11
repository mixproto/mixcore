// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package backoff computes reconnect delays: exponential growth up to a cap,
// with random jitter so that many clients do not reconnect in lockstep.
package backoff

import (
	"errors"
	"math"
	"time"
)

// ErrInvalidPolicy is returned by Validate.
var ErrInvalidPolicy = errors.New("backoff: invalid policy")

// Policy describes the delay before reconnect attempt n (n >= 1).
type Policy struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
	Jitter     float64 // fraction of each delay, 0..1
}

// Default is a starting point (design target, tuned by chaos tests): first
// retry after 100 ms, capped at 2 s, so the console is re-tried at least
// every 2 s while the user wants it. It is not a protocol fact.
var Default = Policy{Initial: 100 * time.Millisecond, Max: 2 * time.Second, Multiplier: 2, Jitter: 0.2}

// Validate reports whether the policy is usable.
func (p Policy) Validate() error {
	if p.Initial <= 0 || p.Max < p.Initial || p.Multiplier < 1 || p.Jitter < 0 || p.Jitter > 1 {
		return ErrInvalidPolicy
	}
	return nil
}

// Delay returns the delay before attempt n. rand01 is a random number in
// [0, 1); passing it in keeps the function pure and testable. The result
// stays within [base*(1-Jitter), min(base*(1+Jitter), Max)] where base is the
// exponential delay capped at Max.
func (p Policy) Delay(attempt int, rand01 float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := float64(p.Initial) * math.Pow(p.Multiplier, float64(attempt-1))
	base = math.Min(base, float64(p.Max))
	spread := base * p.Jitter
	d := base - spread + 2*spread*rand01
	d = math.Min(d, float64(p.Max))
	return time.Duration(math.Max(d, 0))
}
