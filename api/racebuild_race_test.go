// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build race

package api

// raceBuild: the race detector multiplies call costs and make sim-test runs
// every package at once, so latency budgets are asserted only in the
// non-race build (make sim-test runs it separately, one package at a time).
const raceBuild = true
