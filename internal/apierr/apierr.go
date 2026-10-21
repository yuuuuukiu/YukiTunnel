// Package apierr defines the stable, machine-readable error envelope shared by
// the HTTP API, the CLIs and the desktop UIs.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is an API-safe error. Code is stable and meant for programs; Message is
// meant for humans and may be shown verbatim by the UIs.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an API error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// WithDetails attaches structured context (never secrets).
func (e *Error) WithDetails(kv map[string]any) *Error {
	e.Details = kv
	return e
}

// As extracts an *Error from err, falling back to an opaque internal error.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err.Error())
}

func BadRequest(code, msg string) *Error { return New(http.StatusBadRequest, code, msg) }
func Unauthorized(code, msg string) *Error {
	return New(http.StatusUnauthorized, code, msg)
}
func Forbidden(code, msg string) *Error { return New(http.StatusForbidden, code, msg) }
func NotFound(code, msg string) *Error  { return New(http.StatusNotFound, code, msg) }
func Conflict(code, msg string) *Error  { return New(http.StatusConflict, code, msg) }
func TooMany(msg string) *Error {
	return New(http.StatusTooManyRequests, "RATE_LIMITED", msg)
}
func Internal(msg string) *Error {
	return New(http.StatusInternalServerError, "INTERNAL", msg)
}

// Wrapf wraps a low-level error into an internal API error with context.
func Wrapf(err error, format string, args ...any) *Error {
	return Internal(fmt.Sprintf(format, args...) + ": " + err.Error())
}
