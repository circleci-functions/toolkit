package observability

import (
	"context"
	"time"
)

// Reporter interface defines an API for reporting telemetry out of the
// application, such as logging, tracing, metrics, and error reporting
type Reporter interface {
	// Debug logs a message at DEBUG level
	Debug(ctx context.Context, msg string, fields ...Fields)
	// DebugWithError is Debug with an addition error argument that is reported to the error handler at DEBUG level.
	DebugWithError(ctx context.Context, msg string, err error, fields ...Fields)
	// Info logs a message at INFO level.
	Info(ctx context.Context, msg string, fields ...Fields)
	// InfoWithError is Info except it additionally reports the error at INFO level.
	InfoWithError(ctx context.Context, msg string, err error, fields ...Fields)
	// Warn logs a message at WARN level, reports the error at WARN level.
	Warn(ctx context.Context, msg string, fields ...Fields)
	// WarnWithError is Warn with an additional error argument reported to the error handler at WARN level.
	WarnWithError(ctx context.Context, msg string, err error, fields ...Fields)
	// Error logs a message at ERROR level, reports the error to the error handler.
	Error(ctx context.Context, msg string, fields ...Fields)
	// ErrorWithError is Error with an additional error argument. If the error is actually a warning it
	// reroutes to WarnWithError.
	ErrorWithError(ctx context.Context, msg string, err error, fields ...Fields)
	// Panic logs a message at PANIC level, reports an error to rollbar, and aborts the application with a panic.
	Panic(ctx context.Context, msg string, fields ...Fields)
	// PanicWithError is Panic with an additional error argument
	PanicWithError(ctx context.Context, msg string, err error, fields ...Fields)
	// Fatal exits the application and should only be used in main(); consider returning errors or panics instead.
	// Logs a message at FATAL level, reports an error to rollbar, and exits the application.
	Fatal(ctx context.Context, msg string, fields ...Fields)
	// FatalWithError exits the application and should only be used in main(); consider using returned errors or
	// panics instead. Logs a message at FATAL level, reports an error to rollbar, and exits the application.
	FatalWithError(ctx context.Context, msg string, err error, fields ...Fields)

	// ReportRecoveredPanic is a convenience function for ensuring
	// panics are reported to the reporter. Panics after reporting. Only
	// use if this is the last chance to report the panic (like in main)
	// and the panic is not already reported via automatic handling (if
	// you arent using a backplane application).
	//
	//	func main() {
	//  ...
	//		defer reporter.RecoverAndPanic(ctx)
	//  ...
	//	}
	ReportRecoveredPanic(context.Context)

	// Count tracks how many times something happened per second.
	Count(ctx context.Context, name string, value int64, tags ...Fields)
	// Incr is just Count of 1.
	Incr(ctx context.Context, name string, tags ...Fields)
	// Decr is just Count of -1.
	Decr(ctx context.Context, name string, tags ...Fields)
	// Gauge measures the value of a metric at a particular time.
	Gauge(ctx context.Context, name string, value float64, tags ...Fields)
	// Timing sends timing information in milliseconds.
	Timing(ctx context.Context, name string, value time.Duration, tags ...Fields)

	// StartSpan creates a new span using a named Tracer from the global
	// TraceProvider. Returns a child context and function to end the
	// span. The function accepts 0 or 1 error pointers and uses
	// AddResultToSpan before ending.
	//
	// Usage:
	//
	//	func myFunc(...) (err error) {
	//	  ctx, spanEnd := reporter.StartSpan(ctx, "my-func")
	//	  defer spanEnd(&err)
	//	  ...
	//	}
	//
	// The opts are generally expected to be used by other backplane-go packages, rather than application code.
	StartSpan(ctx context.Context, spanName string, opts ...SpanOpt) (context.Context, SpanEndFunc)
	// SetSpanAttribute retrieves a span from the given context and sets string attributes on the span.
	SetSpanAttribute(ctx context.Context, k string, v any)
	// SetSpanAttributes retrieves a span from the given context and sets multiple attributes on the span.
	SetSpanAttributes(ctx context.Context, attrs ...Fields)
	// SetSpanStatusOk retrieves a span from the given context and sets a `Ok` status on the span.
	SetSpanStatusOk(ctx context.Context, desc string)
	// SetSpanStatusError retrieves a span from the given context and sets a `Error` status on the span.
	SetSpanStatusError(ctx context.Context, desc string)
	// AddResultToSpan takes an error modeling the result of the span and
	// uses it to set span status and several result-related fields:
	//   - context canceled/deadline err: SpanStatusOk, result=canceled, warning=err
	//   - IsWarning err: SpanStatusOk, result=warning, warning=err
	//   - all other errs: SpanStatusError, result=error, error=err
	//
	// Prefer using StartSpan with an error argument if possible.
	AddResultToSpan(ctx context.Context, err error)
	// MakeSpanGolden Makes the current span into a golden span
	MakeSpanGolden(ctx context.Context) context.Context
	// TimingFromSpan sends a timing metric from the current span (when it ends), using name as the metric name
	// and attr as the list of fields to use from the span as metric tags. TimingFromSpan can be called any time
	// before the span ends. Subsequent calls the context of the same span will have no effect - use
	// AddTimingTags to add more tags.
	TimingFromSpan(ctx context.Context, name string, attr ...string)
	// AddTimingTags adds more span attribute names that will be added to any metric that it emitted from the span.
	// This will only have an effect after a call to TimingFromSpan (within the same span context).
	AddTimingTags(ctx context.Context, attr ...string)

	// Teardown stops a CompositeReporter
	Teardown(context.Context) error
}

// SpanEndFunc is the type of the function that ends spans; see StartSpan
type SpanEndFunc func(errs ...*error)

// ErrorHandler is an interface for handling errors
type ErrorHandler interface {
	observabilityComponent
	ReportError(ctx context.Context, level string, err error, fields ...Fields)
}

type observabilityComponent interface {
	Describe() Fields
	Teardown(ctx context.Context) error
}
