// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wing

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/model"
	simwing "github.com/mixproto/mixcore/sim/wing"
)

func silences(evs []driver.Event) (silent, responding int) {
	for _, ev := range evs {
		switch ev.(type) {
		case driver.ConsoleSilent:
			silent++
		case driver.ConsoleResponding:
			responding++
		}
	}
	return silent, responding
}

// TestSilence_LocksThenRecoversWithoutReconnect (review go M1): 1.5 s
// without a message locks input (ConsoleSilent, writes refused) but keeps
// the connection; answers end it without a reconnect.
func TestSilence_LocksThenRecoversWithoutReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		r.sim.Pause()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if s, _ := silences(rn.allEvents()); s != 1 {
			t.Fatalf("%d ConsoleSilent after 2 s of silence", s)
		}
		if err := rn.s.Apply(driver.Command{Param: ch1Fader, Value: level(t, -5), Origin: gesture}); !errors.Is(err, driver.ErrStale) {
			t.Fatalf("write while silent: %v", err)
		}
		r.sim.Resume()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if _, resp := silences(rn.allEvents()); resp != 1 {
			t.Fatal("no ConsoleResponding after the console answered again")
		}
		select {
		case err := <-rn.done:
			t.Fatalf("session ended: %v", err)
		default:
		}
		if r.dials != 1 || len(r.sim.Writes()) != 0 {
			t.Fatalf("dials %d, writes %+v", r.dials, r.sim.Writes())
		}
	})
}

// TestSilence_TearsDownAfterDeadAfter: without meters the connection ends
// after 5 s of silence, not at 1.5 s.
func TestSilence_TearsDownAfterDeadAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		rn := r.run(r.connect())
		r.sim.Pause()
		start := time.Now()
		err := <-rn.done
		rn.done <- err
		if took := time.Since(start); !errors.Is(err, driver.ErrConnectionLost) || took < 4*time.Second || took > 6*time.Second {
			t.Fatalf("teardown after %v: %v", took, err)
		}
		r.sim.Resume()
	})
}

// TestSilence_TearsDownEarlyWhenMetersStopToo: TCP and the meter stream
// both silent: the console is gone, no need to wait 5 s.
func TestSilence_TearsDownEarlyWhenMetersStopToo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		s := r.connect()
		rn := r.run(s)
		_ = s.SetMeters(driver.MeterRequest{Inputs: true})
		time.Sleep(3 * time.Second)
		r.sim.Pause()
		start := time.Now()
		err := <-rn.done
		rn.done <- err
		if took := time.Since(start); !errors.Is(err, driver.ErrConnectionLost) || took > 3*time.Second {
			t.Fatalf("teardown after %v: %v", took, err)
		}
		r.sim.Resume()
	})
}

// TestLiveness_PartialTokensDoNotCount (review go L6): bytes that never
// complete a message are not a sign of life.
func TestLiveness_PartialTokensDoNotCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := net.Pipe()
		defer server.Close()
		go func() {
			buf := make([]byte, 64)
			for {
				if _, err := server.Read(buf); err != nil {
					return
				}
			}
		}()
		c, err := dialConn(context.Background(), func(context.Context, string, string) (net.Conn, error) { return client, nil }, "x", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.close()
		base := c.lastRxAt()
		time.Sleep(time.Second)
		_, _ = server.Write(hexBytes(t, "D7 38AE")) // half a hash
		synctest.Wait()
		if !c.lastRxAt().Equal(base) {
			t.Fatal("an incomplete token refreshed liveness")
		}
		_, _ = server.Write(hexBytes(t, "75C2 D5 C1200000"))
		synctest.Wait()
		if !c.lastRxAt().After(base) {
			t.Fatal("a complete message did not refresh liveness")
		}
	})
}

// chaosPipe copies one direction with TCP-like delays: every chunk waits a
// jitter of 0..200 ms, 2 % of them a retransmission timeout more, in order.
func chaosPipe(dst, src net.Conn, rng *rand.Rand) {
	buf := make([]byte, 64<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			_ = dst.Close()
			return
		}
		delay := time.Duration(rng.IntN(200)) * time.Millisecond
		if rng.IntN(100) < 2 {
			delay += 300 * time.Millisecond
		}
		time.Sleep(delay)
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

// TestChaos_LossAndJitter (review go M1): ten minutes over a link with 2 %
// loss and 200 ms jitter: never silent, never torn down, writes settle.
func TestChaos_LossAndJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, simwing.Options{})
		plain := r.opts().Dial
		opts := r.opts()
		opts.Dial = func(ctx context.Context, n, a string) (net.Conn, error) {
			inner, err := plain(ctx, n, a)
			if err != nil {
				return nil, err
			}
			drvSide, mid := net.Pipe()
			// One seeded generator per direction: the chaos is reproducible.
			go chaosPipe(mid, inner, rand.New(rand.NewPCG(1, 2))) //nolint:gosec // G404: reproducible test chaos, not security
			go chaosPipe(inner, mid, rand.New(rand.NewPCG(3, 4))) //nolint:gosec // G404: reproducible test chaos, not security
			return drvSide, nil
		}
		sess, err := r.drv.Connect(context.Background(), driver.Endpoint{Host: consoleIP}, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		rn := r.run(sess.(*session))
		for i := range 60 {
			mustSet(t, r.sim, "/ch/2/fdr", float64(-i))
			time.Sleep(10 * time.Second)
		}
		_ = rn.s.Apply(driver.Command{Param: ref(model.StripInput, 3, model.FieldFader), Value: level(t, -6), Origin: gesture})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if s, _ := silences(rn.allEvents()); s != 0 {
			t.Fatalf("%d silences on a lossy but working link", s)
		}
		select {
		case err := <-rn.done:
			t.Fatalf("session ended: %v", err)
		default:
		}
		if v, _ := r.sim.Value("/ch/3/fdr"); v != -6.0 || r.dials != 1 {
			t.Fatalf("write %v, dials %d", v, r.dials)
		}
	})
}
