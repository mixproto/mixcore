// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/mixproto/mixcore/demo"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/safe"
)

// demoSlot is the engine's one demo mixer (contracts/core-api README, "Demo mixer").
type demoSlot struct {
	mu    sync.Mutex
	mixer *demo.Mixer
	// session: the current (or last) session targets the demo. Written by
	// Start under e.mu, read by the dispatcher for the demo flag.
	session atomic.Bool
}

type demoRequestJSON struct {
	Version     *int   `json:"version"`
	Family      string `json:"family"`
	DeskChanges bool   `json:"deskChanges"`
}

type demoMixerJSON struct {
	Version int    `json:"version"`
	Family  string `json:"family"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Name    string `json:"name"`
}

func parseDemoRequest(data string) (demoRequestJSON, error) {
	var r demoRequestJSON
	if err := decodeStrict(data, &r); err != nil {
		return r, codeErr(CodeInvalidConfig, err)
	}
	if r.Version == nil || *r.Version != contractVersion {
		return r, codeErr(CodeInvalidConfig, errVersion)
	}
	if !knownFamily(r.Family) {
		return r, codeErr(CodeInvalidConfig, fmt.Errorf("unknown family %q", r.Family))
	}
	return r, nil
}

// StartDemo starts the built-in demo mixer (demo.schema.json) and returns
// where it listens (demo-mixer.schema.json). Connect to it with Start. It
// opens one loopback socket and returns at once.
func (e *Engine) StartDemo(requestJSON string) (out string, err error) {
	defer safe.RecoverAsError(&err)
	defer func() { err = mapError(err) }()
	req, err := parseDemoRequest(requestJSON)
	if err != nil {
		return "", err
	}
	drv, ok := e.opts.Drivers[req.Family]
	if !ok {
		return "", codeErr(CodeInvalidConfig, fmt.Errorf("no driver for family %q", req.Family))
	}
	e.demo.mu.Lock()
	defer e.demo.mu.Unlock()
	if e.demo.mixer != nil {
		return "", codeErr(CodeAlreadyStarted, nil)
	}
	m, err := demo.Start(demo.Config{Family: req.Family, Law: drv.FaderLaw(), DeskChanges: req.DeskChanges,
		ListenPacket: e.opts.DemoListenPacket, Logger: e.opts.Logger})
	if errors.Is(err, demo.ErrListen) {
		return "", codeErr(CodeNetwork, err)
	}
	if err != nil {
		return "", err
	}
	ep := m.Endpoint()
	b, err := json.Marshal(demoMixerJSON{Version: contractVersion, Family: m.Family(), Host: ep.Addr().String(),
		Port: int(ep.Port()), Name: m.Name()})
	if err != nil {
		m.Stop()
		return "", err
	}
	e.demo.mixer = m
	return string(b), nil
}

// StopDemo stops a session that targets the demo, then the demo mixer, and
// waits until its port is free and its goroutines have ended.
func (e *Engine) StopDemo() (err error) {
	defer safe.RecoverAsError(&err)
	if e.inOnState() {
		return codeErr(CodeBusy, nil)
	}
	e.stopSession(true)
	e.demo.mu.Lock()
	m := e.demo.mixer
	e.demo.mixer = nil
	e.demo.mu.Unlock()
	if m == nil {
		return codeErr(CodeNotStarted, nil)
	}
	m.Stop()
	return nil
}

// targetsDemo tells whether cfg points at the running demo mixer (family,
// host and port). The demo's address with another family is an invalid config.
func (d *demoSlot) targetsDemo(cfg configJSON) (bool, error) {
	d.mu.Lock()
	m := d.mixer
	d.mu.Unlock()
	if m == nil {
		return false, nil
	}
	ip, err := netip.ParseAddr(cfg.Host)
	if err != nil {
		return false, nil // parseConfig already refused it
	}
	port := cfg.Port
	if port == 0 {
		port = familyPorts[cfg.Family]
	}
	if netip.AddrPortFrom(ip.Unmap(), uint16(port)) != m.Endpoint() {
		return false, nil
	}
	if cfg.Family != m.Family() {
		return false, codeErr(CodeInvalidConfig, fmt.Errorf("the demo mixer is %s, not %s", m.Family(), cfg.Family))
	}
	return true, nil
}

// withoutDemo drops a discovery reply that comes from the demo mixer.
func (d *demoSlot) withoutDemo(found []x32.Found) []x32.Found {
	d.mu.Lock()
	m := d.mixer
	d.mu.Unlock()
	if m == nil {
		return found
	}
	return slices.DeleteFunc(found, func(f x32.Found) bool {
		ip, err := netip.ParseAddr(f.Endpoint.Host)
		return err == nil && netip.AddrPortFrom(ip.Unmap(), uint16(f.Endpoint.Port)) == m.Endpoint()
	})
}

// sessionListen is the socket opener of a session: for the demo, loopback
// sockets without the socket binder (the demo needs no network).
func (e *Engine) sessionListen(isDemo bool) driver.ListenPacketFunc {
	if isDemo {
		return demo.Listen(e.opts.DemoListenPacket)
	}
	return e.opts.ListenPacket
}
