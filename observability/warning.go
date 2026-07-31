package observability

import (
	"context"
	"errors"

	"github.com/circleci-functions/toolkit/internal/semconv"
)

// NewWarning returns an error that declares your application was
// expecting this error and likely gracefully handled it. Warnings are
// sent in telemetry under the "warning" key rather than the "error"
// key. Warnings are reported to the error handler at the "warn" level
// instead of the "error" level.
//
// No two NewWarning errors are equal when tested with Is.
func NewWarning(text string) error {
	return &wrapWarningError{
		msg: text,
		err: errWarning,
	}
}

// sentinel error to use with errors.Is in IsWarning
var errWarning = errors.New("")

// warnable is the structural interface IsWarning checks for after
// the sentinel check fails. Any error type that exposes a
// IsWarning() bool method participates: the outermost match in the
// chain governs the result. The interface is unexported so the
// contract is the method shape, not a named type external packages
// must import.
type warnable interface {
	IsWarning() bool
}

// IsWarning returns true if any error in the chain is a Warning.
// An error is a Warning if it was derived from NewWarning, or if
// any error in the chain implements IsWarning() bool returning
// true (the outermost such error governs).
func IsWarning(err error) bool {
	if errors.Is(err, errWarning) {
		return true
	}
	var w warnable
	if errors.As(err, &w) {
		return w.IsWarning()
	}
	return false
}

// IsWarningNoUnwrap returns true if err itself is a warning. This
// does not check wrapped errors. Use this in Is methods when you
// declare a custom error type that should be considered a warning
// when err is a warning
func IsWarningNoUnwrap(err error) bool {
	//nolint: errorlint // This is intentionally not unwrapping
	return err == errWarning
}

// wrapWarningError is a wrapping error to be tested for being a warning.
type wrapWarningError struct {
	msg string
	err error
}

func (e *wrapWarningError) Error() string {
	return e.msg
}

func (e *wrapWarningError) Unwrap() error {
	return e.err
}

// reportAsWarning returns true if the error has been wrapped in NewWarning
// or if the error is for a cancelled or timed out context.
func reportAsWarning(err error) bool {
	return IsWarning(err) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

type typeError struct {
	err error
	typ string
}

// WithType wraps an error with a type that will be used to set the error.type field in the span.
// This can help avoid having to define a custom error type that implements `interface{ Type() string }`
// to return a low cardinality error type.
// If there are other good reasons for a custom error type, then add the Type method to that type instead.
func WithType(err error, typ string) error {
	return &typeError{
		err: err,
		typ: typ,
	}
}

func (e *typeError) Error() string {
	return e.err.Error()
}

func (e *typeError) Unwrap() error {
	return e.err
}

func (e *typeError) Type() string {
	return e.typ
}

// ErrorType returns the low cardinality error type for the error, and whether that type was explicit. A type is
// explicit when an error in the chain declared it (via a Type method) or when it is a recognised default. It is not
// explicit when it had to be derived from the Go type of the error. Callers that expose the type on a public surface
// should only use it when explicit is true, to avoid leaking internal Go type names.
func ErrorType(err error) (string, bool) {
	return semconv.Type(err)
}
