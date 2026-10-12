// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mobile

// StartDemo starts the built-in demo mixer (demo.schema.json) and returns
// where it listens (demo-mixer.schema.json); pass its family, host and port
// to Start. Every state message of that session carries "demo": true.
// ERR_ALREADY_STARTED while a demo runs. Never blocks on the network.
func StartDemo(requestJSON string) (mixer string, err error) {
	defer recoverErr(&err)
	return engine.StartDemo(requestJSON)
}

// StopDemo stops a session to the demo mixer, then the demo mixer; its port
// and goroutines are gone when it returns. ERR_NOT_STARTED when none runs.
func StopDemo() (err error) {
	defer recoverErr(&err)
	return engine.StopDemo()
}
