// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/meters"
)

// Redesign CC-12 (core-api v0.5 rule 16, CR-31, AC-RD-S04): only the owner
// locks show mode on with a 4-digit PIN; while locked, turning show mode
// off and unlocking Gain are ErrRole, lowering Gain stays live; the right
// PIN unlocks; 5 wrong PINs lock the PIN out for 30 s; the PIN is kept
// only as a salted hash.
func TestShowLock_OwnerLocksWithAPIN(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, drv := startSafe(t, SafetyConfig{}, safetyState(-6))
		_ = sup.SetClientRole(engineerB.ClientID, Role{Kind: RoleEngineer})
		if _, err := sup.LockShowMode(engineerB, "1234"); !errors.Is(err, ErrRole) {
			t.Fatalf("an engineer locks: %v", err)
		}
		if _, err := sup.LockShowMode(gesture, "12a4"); err == nil {
			t.Fatal("a PIN that is not 4 digits")
		}
		rec, err := sup.LockShowMode(gesture, "1234")
		if err != nil || !strings.HasPrefix(rec, "pbkdf2-sha256$") || strings.Contains(rec, "1234") {
			t.Fatalf("lock: %q %v", rec, err)
		}
		if u := sup.Drain(); !u.ShowMode || !u.ShowModeLocked {
			t.Fatalf("show mode %v locked %v", u.ShowMode, u.ShowModeLocked)
		}
		if err := sup.SetShowModeBy(gesture, false); !errors.Is(err, ErrRole) {
			t.Fatalf("show mode off while locked: %v", err)
		}
		if err := sup.Unlock(gain3, gesture); !errors.Is(err, ErrRole) {
			t.Fatalf("Gain unlock while locked: %v", err)
		}
		if _, err := sup.Write(set(gain3, lvl(t, 31.5), gesture)); err != nil || lastWrite(drv) != 31.5 {
			t.Fatalf("-1 while locked: %v", err)
		}
		if _, err := sup.LockShowMode(gesture, "9999"); !errors.Is(err, ErrRole) {
			t.Fatalf("lock again while locked: %v", err)
		}
		if err := sup.UnlockShowMode(engineerB, "1234"); err != nil {
			t.Fatalf("the right PIN: %v", err)
		}
		if u := sup.Drain(); !u.ShowMode || u.ShowModeLocked {
			t.Fatalf("after the PIN: show mode %v locked %v", u.ShowMode, u.ShowModeLocked)
		}
		if err := sup.SetShowModeBy(gesture, false); err != nil {
			t.Fatal(err)
		}
	})
}

func TestShowLock_WrongPINsLockOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		if _, err := sup.LockShowMode(gesture, "2468"); err != nil {
			t.Fatal(err)
		}
		for range 5 {
			if err := sup.UnlockShowMode(gesture, "0000"); !errors.Is(err, ErrRole) {
				t.Fatalf("wrong PIN: %v", err)
			}
		}
		if err := sup.UnlockShowMode(gesture, "2468"); !errors.Is(err, ErrRole) {
			t.Fatalf("the right PIN during the lockout: %v", err)
		}
		time.Sleep(30*time.Second + time.Millisecond)
		if err := sup.UnlockShowMode(gesture, "2468"); err != nil {
			t.Fatalf("after 30 s: %v", err)
		}
	})
}

// The record restores the lock at the next start; a broken record is
// refused; the internal SetShowMode cannot turn a locked show mode off.
func TestShowLock_RecordRestoresTheLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sup, _ := startSafe(t, SafetyConfig{}, safetyState(-6))
		rec, _ := sup.LockShowMode(gesture, "1357")
		sup.Stop()
		drv := &fakeDriver{snapshot: safetyState(-6), caps: gainCaps}
		if _, err := New(Config{Driver: drv, Safety: SafetyConfig{ShowModeLock: "pbkdf2-sha256$1$x$y"}}); err == nil {
			t.Fatal("a broken record accepted")
		}
		again, err := New(Config{Driver: drv, Rand: func() float64 { return 0.5 }, Options: driver.Options{Meters: &meters.Slot{}},
			Safety: SafetyConfig{ShowModeLock: rec}})
		if err != nil {
			t.Fatal(err)
		}
		_ = again.Start(context.Background())
		t.Cleanup(again.Stop)
		synctest.Wait()
		again.SetShowMode(false)
		if u := again.Drain(); !u.ShowMode || !u.ShowModeLocked {
			t.Fatalf("restored: show mode %v locked %v", u.ShowMode, u.ShowModeLocked)
		}
		if err := again.UnlockShowMode(gesture, "1357"); err != nil {
			t.Fatalf("the PIN of the record: %v", err)
		}
	})
}
