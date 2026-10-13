// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !race

package api

// raceBuild is false: latency budgets are asserted (racebuild_race_test.go).
const raceBuild = false
