// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package console

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mixproto/mixcore/driver"
)

// Show mode locked by a PIN (redesign CC-12, S8, CR-31, core-api v0.5 rule
// 16). The owner locks show mode on with a 4-digit PIN for volunteers;
// while locked, turning show mode off and unlocking Gain are ErrRole. The
// PIN is kept only as a salted PBKDF2-SHA256 record, which the app stores
// and gives back in the config. A 4-digit PIN cannot resist an offline
// guess of the record; the app keeps it in protected storage, and online
// guesses are limited here.
const (
	pinDigits        = 4
	pinRecordPrefix  = "pbkdf2-sha256"
	pinIterations    = 20000 // about 10 ms on a phone; the record is no secret vault (see above)
	pinMinIterations = 10000 // a record below it is refused
	pinMaxIterations = 1000000
	pinSaltBytes     = 16
	pinKeyBytes      = 32
)

// ErrPINRecord: a show-mode PIN record the core cannot read.
var ErrPINRecord = errors.New("console: invalid show mode PIN record")

// ValidPIN reports exactly 4 ASCII digits.
func ValidPIN(pin string) bool {
	if len(pin) != pinDigits {
		return false
	}
	for _, c := range []byte(pin) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type pinRecord struct {
	iter      int
	salt, key []byte
}

var b64 = base64.RawURLEncoding

// newPINRecord hashes pin with a fresh random salt.
func newPINRecord(pin string) (pinRecord, error) {
	salt := make([]byte, pinSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return pinRecord{}, fmt.Errorf("console: PIN salt: %w", err)
	}
	key, err := pinHash(pin, salt, pinIterations)
	if err != nil {
		return pinRecord{}, fmt.Errorf("console: PIN hash: %w", err)
	}
	return pinRecord{iter: pinIterations, salt: salt, key: key}, nil
}

// String is the stored form: pbkdf2-sha256$<iterations>$<salt>$<key>.
func (r pinRecord) String() string {
	return strings.Join([]string{pinRecordPrefix, strconv.Itoa(r.iter), b64.EncodeToString(r.salt), b64.EncodeToString(r.key)}, "$")
}

// parsePINRecord reads a stored record; anything else is ErrPINRecord.
func parsePINRecord(s string) (pinRecord, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 4 || parts[0] != pinRecordPrefix {
		return pinRecord{}, ErrPINRecord
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < pinMinIterations || iter > pinMaxIterations {
		return pinRecord{}, ErrPINRecord
	}
	salt, err1 := b64.DecodeString(parts[2])
	key, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(salt) != pinSaltBytes || len(key) != pinKeyBytes {
		return pinRecord{}, ErrPINRecord
	}
	return pinRecord{iter: iter, salt: salt, key: key}, nil
}

func (r pinRecord) matches(pin string) bool {
	key, err := pinHash(pin, r.salt, r.iter)
	return err == nil && subtle.ConstantTimeCompare(key, r.key) == 1
}

// pinHash derives the key of a PIN (a variable so tests can count hashes).
var pinHash = func(pin string, salt []byte, iter int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pin, salt, iter, pinKeyBytes)
}

// showLock is the PIN lock state. Guarded by Supervisor.mu.
type showLock struct {
	record *pinRecord // nil: not locked
	guard  pinGuard   // online guessing limits (pinguard.go)
}

// LockShowMode turns show mode on and locks it with pin (owner only) and
// returns the record to store. It ends every Gain unlock and its undo grace
// (review sec M2). It writes nothing.
func (s *Supervisor) LockShowMode(origin driver.Origin, pin string) (string, error) {
	if !origin.MayWrite() {
		return "", driver.ErrNotUserOrigin
	}
	if !ValidPIN(pin) {
		return "", fmt.Errorf("%w: the PIN must be 4 digits", ErrPINRecord)
	}
	s.mu.Lock()
	locked, role := s.lock.record != nil, s.safe.role(origin.ClientID)
	s.mu.Unlock()
	if role.Kind != RoleOwner || locked {
		return "", fmt.Errorf("%w: only the owner locks show mode, once", ErrRole)
	}
	rec, err := newPINRecord(pin) // the hash runs outside mu
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock.record != nil || s.safe.role(origin.ClientID).Kind != RoleOwner { // checked again: the hash ran outside mu
		return "", fmt.Errorf("%w: only the owner locks show mode, once", ErrRole)
	}
	s.lock = showLock{record: &rec}
	s.safe.showMode = true
	s.safe.unlocks.endAll(UnlockShowModeLocked)
	clear(s.safe.unlocks.relocked) // no undo grace survives the lock (sec M2)
	s.signal()
	return rec.String(), nil
}

// UnlockShowMode removes the PIN lock with the right PIN (owner or
// engineer). Show mode stays on. A wrong PIN is ErrRole; the attempt is
// reserved before the hash and counted per client and in total
// (pinguard.go).
func (s *Supervisor) UnlockShowMode(origin driver.Origin, pin string) error {
	if !origin.MayWrite() {
		return driver.ErrNotUserOrigin
	}
	s.mu.Lock()
	rec, c, err := s.reservePINLocked(origin)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	ok := ValidPIN(pin) && rec.matches(pin) // the hash runs outside mu, one at a time
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock.record != rec { // relocked or unlocked meanwhile: the attempt counts for nothing
		s.lock.guard.hashing = false
		return fmt.Errorf("%w: the show mode lock changed", ErrRole)
	}
	s.settlePINLocked(origin, c, !ok)
	if !ok {
		return fmt.Errorf("%w: wrong PIN", ErrRole)
	}
	s.lock = showLock{}
	s.signal()
	return nil
}

// showLockedLocked reports the PIN lock. Callers hold mu.
func (s *Supervisor) showLockedLocked() bool { return s.lock.record != nil }
