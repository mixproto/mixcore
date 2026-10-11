// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"errors"

	"github.com/mixproto/mixcore/console"
	"github.com/mixproto/mixcore/driver"
	"github.com/mixproto/mixcore/internal/safe"
	"github.com/mixproto/mixcore/model"
)

// Stable error codes (contracts/core-api/README.md). Error() of every error
// the API returns is exactly one of these.
const (
	CodeInvalidConfig  = "ERR_INVALID_CONFIG"
	CodeInvalidCommand = "ERR_INVALID_COMMAND"
	CodeInvalidValue   = "ERR_INVALID_VALUE"
	CodeNotUserOrigin  = "ERR_NOT_USER_ORIGIN"
	CodeStale          = "ERR_STALE"
	CodeGestureEnded   = "ERR_GESTURE_ENDED"
	CodeUnsupported    = "ERR_UNSUPPORTED"
	CodeNotStarted     = "ERR_NOT_STARTED"
	CodeAlreadyStarted = "ERR_ALREADY_STARTED"
	CodeInternal       = "ERR_INTERNAL"
	CodeRole           = "ERR_ROLE"             // reserved for MixLink roles, not emitted in v0
	CodeBusy           = "ERR_BUSY"             // a discovery is already running
	CodeNetwork        = "ERR_NETWORK"          // no socket could be opened or no probe sent (discovery result)
	CodeInvalidArg     = "ERR_INVALID_ARGUMENT" // bindings only: NULL pointer or bad length from native code
)

// Error carries a stable code for the bindings and the cause for logs.
type Error struct {
	Code  string
	Cause error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

func codeErr(code string, cause error) error { return &Error{Code: code, Cause: cause} }

// CodeOf returns the stable code of err ("" for nil).
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// mapError turns core errors into coded API errors.
func mapError(err error) error {
	var e *Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &e):
		return err
	case errors.Is(err, console.ErrGestureEnded):
		return codeErr(CodeGestureEnded, err)
	case errors.Is(err, driver.ErrNotUserOrigin):
		return codeErr(CodeNotUserOrigin, err)
	case errors.Is(err, driver.ErrStale), errors.Is(err, driver.ErrClosed):
		return codeErr(CodeStale, err)
	case errors.Is(err, driver.ErrUnsupported), errors.Is(err, model.ErrUnknownParam):
		return codeErr(CodeUnsupported, err)
	case errors.Is(err, model.ErrInvalidValue):
		return codeErr(CodeInvalidValue, err)
	case errors.Is(err, safe.ErrPanic):
		return codeErr(CodeInternal, err)
	default:
		return codeErr(CodeInternal, err)
	}
}
