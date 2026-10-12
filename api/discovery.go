// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mixproto/mixcore/driver/x32"
	"github.com/mixproto/mixcore/internal/safe"
)

// DiscoveryListener receives one result message per Discover call
// (contracts/core-api/discovery.schema.json) from the discovery goroutine.
// It must return quickly; it may call Discover again.
type DiscoveryListener interface {
	OnDiscovery(resultJSON string)
}

// Discovery limits (contracts/core-api/discover.schema.json).
const (
	defaultDiscoverWait = 1500 * time.Millisecond
	minDiscoverWaitMs   = 100
	maxDiscoverWaitMs   = 10000
	maxDiscoverHosts    = 32
)

type discoveryListenerBox struct{ l DiscoveryListener }

// discovery is the engine's single discovery slot: at most one probe runs.
type discovery struct {
	lis     atomic.Pointer[discoveryListenerBox]
	running atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type hostJSON struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
}

type discoverJSON struct {
	Version   *int       `json:"version"`
	Family    string     `json:"family"`
	Broadcast bool       `json:"broadcast"`
	Hosts     []hostJSON `json:"hosts,omitempty"`
	WaitMs    int        `json:"waitMs,omitempty"`
}

type discoverRequest struct {
	family    string
	manual    []netip.AddrPort
	broadcast bool
	wait      time.Duration
}

type foundJSON struct {
	Family   string `json:"family"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	Model    string `json:"model"`
	Firmware string `json:"firmware"`
}

type discoveryResultJSON struct {
	Version  int         `json:"version"`
	Type     string      `json:"type"`
	Status   string      `json:"status"`
	Error    string      `json:"error,omitempty"`
	Consoles []foundJSON `json:"consoles"`
}

func parseDiscover(data string) (discoverRequest, error) {
	var d discoverJSON
	if err := decodeStrict(data, &d); err != nil {
		return discoverRequest{}, codeErr(CodeInvalidConfig, err)
	}
	if d.Version == nil || *d.Version != contractVersion {
		return discoverRequest{}, codeErr(CodeInvalidConfig, errVersion)
	}
	if !knownFamily(d.Family) {
		return discoverRequest{}, codeErr(CodeInvalidConfig, fmt.Errorf("unknown family %q", d.Family))
	}
	if len(d.Hosts) > maxDiscoverHosts || (!d.Broadcast && len(d.Hosts) == 0) {
		return discoverRequest{}, codeErr(CodeInvalidConfig, fmt.Errorf("%d hosts, broadcast %v", len(d.Hosts), d.Broadcast))
	}
	req := discoverRequest{family: d.Family, broadcast: d.Broadcast, wait: defaultDiscoverWait}
	if d.WaitMs != 0 {
		if d.WaitMs < minDiscoverWaitMs || d.WaitMs > maxDiscoverWaitMs {
			return discoverRequest{}, codeErr(CodeInvalidConfig, fmt.Errorf("waitMs %d", d.WaitMs))
		}
		req.wait = time.Duration(d.WaitMs) * time.Millisecond
	}
	for _, h := range d.Hosts {
		ap, err := hostAddrPort(h, familyPorts[d.Family])
		if err != nil {
			return discoverRequest{}, codeErr(CodeInvalidConfig, err)
		}
		req.manual = append(req.manual, ap)
	}
	return req, nil
}

func hostAddrPort(h hostJSON, defaultPort int) (netip.AddrPort, error) {
	ip, err := netip.ParseAddr(h.Host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("host must be an IP address: %w", err)
	}
	port := h.Port
	if port == 0 {
		port = defaultPort
	}
	if port < 1 || port > math.MaxUint16 {
		return netip.AddrPort{}, fmt.Errorf("port %d", h.Port)
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), nil
}

// SetDiscoveryListener sets (or clears, with nil) the discovery listener.
func (e *Engine) SetDiscoveryListener(l DiscoveryListener) {
	if l == nil {
		e.disc.lis.Store(nil)
		return
	}
	e.disc.lis.Store(&discoveryListenerBox{l: l})
}

// Discover starts one probe for consoles (broadcast on every interface and/or
// manual IPs) and returns at once; the result goes to the DiscoveryListener.
// A probe only reads (X32-F12, XA-F12): it never subscribes or writes. While one runs,
// Discover returns ERR_BUSY.
func (e *Engine) Discover(requestJSON string) (err error) {
	defer safe.RecoverAsError(&err)
	defer func() { err = mapError(err) }()
	req, err := parseDiscover(requestJSON)
	if err != nil {
		return err
	}
	if !e.disc.running.CompareAndSwap(false, true) {
		return codeErr(CodeBusy, nil)
	}
	onPanic := func(err error) {
		e.disc.running.Store(false)
		e.opts.Logger.Error("api: discovery panicked", "err", err)
	}
	safe.Go(&e.disc.wg, onPanic, func() { e.runDiscovery(req) })
	return nil
}

func (e *Engine) runDiscovery(req discoverRequest) {
	targets := slices.Clone(req.manual)
	if req.broadcast {
		targets = append(targets, broadcastTargets(req.family)...)
	}
	found, err := x32.Probe(e.disc.ctx, e.opts.ListenPacket, targets, req.wait) // the same /xinfo probe for both families
	found = e.demo.withoutDemo(found)                                           // the demo is never a real console
	msg := encodeDiscovery(req.family, found, err)
	if err != nil {
		e.opts.Logger.Info("api: discovery failed", "err", err)
	}
	e.disc.running.Store(false) // before delivery: the listener may start the next probe
	box := e.disc.lis.Load()
	if box == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			e.opts.Logger.Error("api: discovery listener panicked", "panic", r)
		}
	}()
	box.l.OnDiscovery(msg)
}

// encodeDiscovery lists the replies classified as family (classify).
func encodeDiscovery(family string, found []x32.Found, probeErr error) string {
	out := discoveryResultJSON{Version: contractVersion, Type: "discovery", Status: "done", Consoles: []foundJSON{}}
	if probeErr != nil {
		out.Status, out.Error = "failed", CodeNetwork
	}
	for _, f := range found {
		if classify(f) != family {
			continue
		}
		out.Consoles = append(out.Consoles, foundJSON{
			Family: family, Host: f.Endpoint.Host, Port: f.Endpoint.Port,
			Name: f.Identity.Name, Model: f.Identity.Model, Firmware: f.Identity.Firmware,
		})
	}
	b, err := json.Marshal(out)
	if err != nil { // unreachable: plain strings and ints
		return `{"version":0,"type":"discovery","status":"failed","error":"` + CodeInternal + `","consoles":[]}`
	}
	return string(b)
}

// stop cancels a running probe and waits for its goroutine.
func (d *discovery) stop() {
	d.cancel()
	d.wg.Wait()
}
