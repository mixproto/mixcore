// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
)

// Review of v0.5 (security M1, go S5, sec L3, sec M2; lead decisions):
// online PIN guessing is bounded per client and in total, attempts are
// reserved before the hash, lockouts grow, the owner is never locked out by
// other clients, and locking show mode ends every Gain unlock.

// countHashes counts PIN hashes for the duration of a test.
func countHashes(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	orig := pinHash
	pinHash = func(pin string, salt []byte, iter int) ([]byte, error) {
		n.Add(1)
		return orig(pin, salt, iter)
	}
	t.Cleanup(func() { pinHash = orig })
	return &n
}

func seat(id string) driver.Origin { return driver.Origin{Kind: driver.OriginGesture, ClientID: id} }

// 200 parallel guesses from one seat: at most 5 are hashed, then the seat
// is locked out; the owner unlocks during that lockout.
func TestPINGuard_ParallelGuessesAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.SetClientRole("eng", Role{Kind: RoleEngineer})
		if _, err := sup.LockShowMode(gesture, "2468"); err != nil {
			t.Fatal(err)
		}
		hashes := countHashes(t)
		var wg sync.WaitGroup
		for i := range 200 {
			wg.Go(func() { _ = sup.UnlockShowMode(seat("eng"), fmt.Sprintf("%04d", i%2000+3000)) })
		}
		wg.Wait()
		for range 10 { // sequential retries still refused without hashing
			_ = sup.UnlockShowMode(seat("eng"), "0000")
		}
		if n := hashes.Load(); n > pinMaxFailures {
			t.Fatalf("%d PINs hashed, want at most %d", n, pinMaxFailures)
		}
		if err := sup.UnlockShowMode(seat("eng"), "2468"); !errors.Is(err, ErrRole) {
			t.Fatalf("the right PIN from the locked-out seat: %v", err)
		}
		if err := sup.UnlockShowMode(gesture, "2468"); err != nil { // the owner (sec L3)
			t.Fatalf("the owner during another seat's lockout: %v", err)
		}
	})
}

// Many seats share a higher limit; the owner is exempt from it.
func TestPINGuard_SharedLimitAcrossSeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		_, _ = sup.LockShowMode(gesture, "2468")
		hashes := countHashes(t)
		for i := range 40 {
			id := fmt.Sprintf("seat-%02d", i)
			_ = sup.SetClientRole(id, Role{Kind: RoleEngineer})
			_ = sup.UnlockShowMode(seat(id), "1111")
		}
		if n := hashes.Load(); n > pinSharedMaxFailures {
			t.Fatalf("%d PINs hashed across seats, want at most %d", n, pinSharedMaxFailures)
		}
		_ = sup.SetClientRole("fresh", Role{Kind: RoleEngineer})
		if err := sup.UnlockShowMode(seat("fresh"), "2468"); !errors.Is(err, ErrRole) {
			t.Fatalf("a fresh seat during the shared lockout: %v", err)
		}
		if err := sup.UnlockShowMode(gesture, "2468"); err != nil {
			t.Fatalf("the owner during the shared lockout: %v", err)
		}
	})
}

// Lockouts grow: 30 s, then 60 s, ... at most 10 minutes.
func TestPINGuard_LockoutsGrow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.SetClientRole("eng", Role{Kind: RoleEngineer})
		_, _ = sup.LockShowMode(gesture, "2468")
		fail := func() {
			for range pinMaxFailures {
				_ = sup.UnlockShowMode(seat("eng"), "0000")
			}
		}
		fail()
		time.Sleep(30*time.Second + time.Millisecond)
		fail() // the second lockout: 60 s
		time.Sleep(59 * time.Second)
		if err := sup.UnlockShowMode(seat("eng"), "2468"); !errors.Is(err, ErrRole) {
			t.Fatalf("59 s into the second lockout: %v", err)
		}
		time.Sleep(time.Second + time.Millisecond)
		if err := sup.UnlockShowMode(seat("eng"), "2468"); err != nil {
			t.Fatalf("after the 60 s lockout: %v", err)
		}
		if d := pinLockout(20); d != pinLockoutMax {
			t.Fatalf("lockout cap %v", d)
		}
	})
}

// Sec M2 (the reviewer's PoC): locking show mode ends every Gain unlock and
// its undo grace, so an engineer cannot raise Gain after the PIN lock.
func TestShowLock_LockEndsGainUnlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{ShowMode: true}, safetyState(-6))
		eng := seat("eng")
		_ = sup.SetClientRole("eng", Role{Kind: RoleEngineer})
		if err := sup.Unlock(gain3, eng); err != nil {
			t.Fatal(err)
		}
		_, _ = sup.Write(set(gain3, lvl(t, 33.5), eng))
		pushGain(t, drv, 33.5, eng, false)
		_, _ = sup.Write(set(gain3, lvl(t, 31.5), eng))
		pushGain(t, drv, 31.5, eng, false)
		if _, err := sup.LockShowMode(gesture, "2468"); err != nil {
			t.Fatal(err)
		}
		u := sup.Drain()
		if len(u.Unlocked) != 0 || !slices.Contains(u.UnlockEnded, UnlockEvent{Headamp: 3, Reason: UnlockShowModeLocked}) {
			t.Fatalf("unlocked %v, ended %+v", u.Unlocked, u.UnlockEnded)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 32.5), eng)); !errors.Is(err, ErrShowMode) {
			t.Fatalf("an engineer raises Gain after the PIN lock: %v", err)
		}
		if _, err := sup.Write(Write{Command: driver.Command{Param: gain3, Value: lvl(t, 33.5), Origin: eng}, Undo: true}); !errors.Is(err, ErrShowMode) {
			t.Fatalf("the undo grace survived the PIN lock: %v", err)
		}
	})
}
