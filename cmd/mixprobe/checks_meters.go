// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"time"

	"github.com/mixproto/mixcore/internal/osc"
)

const (
	meterSample     = 2 * time.Second  // how long the rate is measured
	meterLifeWait   = 13 * time.Second // longer than the documented 10 s lifetime (X32-F50)
	meterInterval   = 50 * time.Millisecond
	meterTTL        = 10 * time.Second
	meterTolerance  = 0.25 // fraction allowed around the documented interval and lifetime
	bank1Count      = 96   // X32-F51
	bank2Count      = 49   // X32-F55
	bank2MainL      = 22   // X32-F55
	bank1Slashed    = "/meters/1"
	bank1Unslashed  = "meters/1"
	bank2           = "/meters/2"
	meterCountBytes = 4
)

// meterRun is what one meter request produced on its own socket.
type meterRun struct {
	packets  []rxMsg
	count    int // the blob's value count of the first packet
	interval time.Duration
}

// requestMeters asks for a bank on a fresh socket and collects for d.
func (p *prober) requestMeters(ctx context.Context, bank, reply string, d time.Duration) (meterRun, error) {
	return p.requestMeterArgs(ctx, reply, d, osc.String(bank))
}

// requestMeterForm sends /meters with args (the first is the bank, whose
// address the replies carry) on a fresh socket and collects for d.
func (p *prober) requestMeterForm(ctx context.Context, d time.Duration, args ...osc.Arg) (meterRun, error) {
	return p.requestMeterArgs(ctx, string(args[0].Bytes), d, args...)
}

func (p *prober) requestMeterArgs(ctx context.Context, reply string, d time.Duration, args ...osc.Arg) (meterRun, error) {
	c, err := openRaw(ctx, p.cfg.listen, p.cfg.console)
	if err != nil {
		return meterRun{}, err
	}
	defer c.close()
	if err := c.send("/meters", args...); err != nil {
		return meterRun{}, err
	}
	msgs, err := c.collect(d)
	if err != nil {
		return meterRun{}, err
	}
	var r meterRun
	for _, m := range msgs {
		if m.Address == reply && m.HasArg && m.Arg.Type == osc.TypeBlob {
			r.packets = append(r.packets, m)
		}
	}
	if len(r.packets) > 0 && len(r.packets[0].Arg.Bytes) >= meterCountBytes {
		r.count = int(binary.LittleEndian.Uint32(r.packets[0].Arg.Bytes))
	}
	r.interval = medianGap(r.packets)
	return r, nil
}

func medianGap(ms []rxMsg) time.Duration {
	if len(ms) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(ms)-1)
	for i := 1; i < len(ms); i++ {
		gaps = append(gaps, ms[i].At.Sub(ms[i-1].At))
	}
	slices.Sort(gaps)
	return gaps[len(gaps)/2]
}

func near(got, want time.Duration) bool {
	d := float64(got - want)
	return d >= -meterTolerance*float64(want) && d <= meterTolerance*float64(want)
}

// checkMeters measures bank 1 (rate, count, lifetime), the unslashed bank
// name and bank 2 (Main L/R), each on its own short-lived socket.
func (p *prober) checkMeters(ctx context.Context) error {
	start := time.Now()
	r1, err := p.requestMeters(ctx, bank1Slashed, bank1Slashed, meterLifeWait)
	if err != nil {
		return err
	}
	p.add(bank1Finding(r1, start))
	alt, err := p.requestMeters(ctx, bank1Unslashed, bank1Slashed, meterSample)
	if err != nil {
		return err
	}
	p.add(Finding{ID: "meters-bank-name", Facts: []string{"X32-F52"}, Title: "the bank name without the leading slash is accepted",
		Status: Measured, Detail: fmt.Sprintf("/meters ,s %q produced %d packet(s) in %s", bank1Unslashed, len(alt.packets), meterSample)})
	r2, err := p.requestMeters(ctx, bank2, bank2, meterSample)
	if err != nil {
		return err
	}
	p.add(bank2Finding(r2))
	return nil
}

func bank1Finding(r meterRun, start time.Time) Finding {
	f := Finding{ID: "meters-bank1", Facts: []string{"X32-F50", "X32-F51", "X32-F53"},
		Title: "bank /meters/1: 96 values every 50 ms for 10 s", Evidence: map[string]string{}}
	if len(r.packets) == 0 {
		f.Status, f.Detail = Refuted, "no /meters/1 packet at all"
		return f
	}
	life := r.packets[len(r.packets)-1].At.Sub(start)
	f.Evidence["packets"] = fmt.Sprint(len(r.packets))
	f.Evidence["count"] = fmt.Sprint(r.count)
	f.Evidence["median interval"] = ms(r.interval)
	f.Evidence["last packet after request"] = ms(life)
	f.Detail = fmt.Sprintf("count %d, median interval %s, stream stopped %s after a single request", r.count, ms(r.interval), ms(life))
	f.Status = Confirmed
	if r.count != bank1Count || !near(r.interval, meterInterval) || !near(life, meterTTL) {
		f.Status = Refuted
	}
	return f
}

func bank2Finding(r meterRun) Finding {
	f := Finding{ID: "meters-bank2", Facts: []string{"X32-F55"}, Title: "bank /meters/2: 49 values, Main L/R at 22/23"}
	if len(r.packets) == 0 {
		f.Status, f.Detail = Refuted, "no /meters/2 packet"
		return f
	}
	f.Detail = fmt.Sprintf("count %d, median interval %s", r.count, ms(r.interval))
	f.Status = Confirmed
	if r.count != bank2Count {
		f.Status = Refuted
		return f
	}
	b := r.packets[len(r.packets)-1].Arg.Bytes
	if off := meterCountBytes + 4*(bank2MainL+1); len(b) >= off+4 {
		l := binary.LittleEndian.Uint32(b[meterCountBytes+4*bank2MainL:])
		rr := binary.LittleEndian.Uint32(b[off:])
		f.Evidence = map[string]string{"main L/R raw": fmt.Sprintf("%#08x %#08x (feed a signal to the main bus to see them move)", l, rr)}
	}
	return f
}
