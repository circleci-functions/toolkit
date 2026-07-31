package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/circleci-functions/toolkit/internal/report"
	"github.com/circleci-functions/toolkit/internal/semconv"
	"github.com/circleci-functions/toolkit/observability/noop"
)

// CompositeReporter is a Reporter component that manages high level
// observability information from the environment by delegating each reporting
// function to the appropriate sub-struct
type CompositeReporter struct {
	baseFields Fields

	logger       report.LogEmitter
	metrics      report.MetricEmitter
	errorHandler report.ErrorEmitter
	tracer       report.TraceEmitter
	eventHook    eventHook
}

// CompositeReporterOption is a function that modifies a CompositeReporter
type CompositeReporterOption func(r *CompositeReporter)

// NewCompositeReporter creates a reporter from options. You likely want
// NewReporter to create a production reporter.
func NewCompositeReporter(opts ...CompositeReporterOption) *CompositeReporter {
	baseFields := Fields{}
	reporter := &CompositeReporter{
		baseFields:   baseFields,
		logger:       noop.Emitter,
		errorHandler: noop.Emitter,
		metrics:      noop.Emitter,
		tracer:       noop.Emitter,
	}

	for _, opt := range opts {
		opt(reporter)
	}
	return reporter
}

// WithBaseFields modifies the baseFields of a CompositeReporter
func WithBaseFields(baseFields Fields) CompositeReporterOption {
	return func(r *CompositeReporter) {
		r.baseFields = baseFields
	}
}

// WithLogger uses a custom Logger in a CompositeReporter
func WithLogger(logger report.LogEmitter) CompositeReporterOption {
	return func(r *CompositeReporter) {
		r.logger = logger
	}
}

// WithErrorHandler uses a custom ErrorHandler in a CompositeReporter
func WithErrorHandler(errorHandler report.ErrorEmitter) CompositeReporterOption {
	return func(r *CompositeReporter) {
		r.errorHandler = errorHandler
	}
}

// WithMetricsEmitter uses a custom MetricsEmitter in a CompositeReporter
func WithMetricsEmitter(metrics report.MetricEmitter) CompositeReporterOption {
	return func(r *CompositeReporter) {
		r.metrics = metrics
	}
}

// WithTracer uses a custom Tracer in a CompositeReporter
func WithTracer(tracer report.TraceEmitter) CompositeReporterOption {
	return func(r *CompositeReporter) {
		r.tracer = tracer
	}
}

type eventHook func(lvl report.Level, msg string, err error, fields Fields)

// WithEventHook can be used to capture the events called on the reporter.
func WithEventHook(f eventHook) CompositeReporterOption {
	return func(r *CompositeReporter) {
		r.eventHook = f
	}
}

// Teardown stops a CompositeReporter
func (r *CompositeReporter) Teardown(ctx context.Context) error {
	err := r.teardownExceptLogs(ctx)
	// N.B. If logs ever need a teardown it should be added here
	return err
}

// BaseFields returns base Fields that should be included in all telemetry data
func (r *CompositeReporter) BaseFields() Fields {
	return r.baseFields
}

// Debug logs a message at DEBUG level
func (r *CompositeReporter) Debug(ctx context.Context, msg string, fields ...Fields) {
	r.event(ctx, report.DebugLevel, msg, nil, fields...)
}

// DebugWithError is Debug with an addition error argument that is reported to the error
// handler at DEBUG level
func (r *CompositeReporter) DebugWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	r.event(ctx, report.DebugLevel, msg, err, fields...)
}

// Info logs a message at INFO level and adds a trace event to the running span
func (r *CompositeReporter) Info(ctx context.Context, msg string, fields ...Fields) {
	r.event(ctx, report.InfoLevel, msg, nil, fields...)
}

// InfoWithError is Info with an additional error argument that is reported to the error
// handler at INFO level
func (r *CompositeReporter) InfoWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	r.event(ctx, report.InfoLevel, msg, err, fields...)
}

// Warn logs a message at WARN level, reports the error to the error handler,
// and adds a trace event to the running span
func (r *CompositeReporter) Warn(ctx context.Context, msg string, fields ...Fields) {
	r.event(ctx, report.WarnLevel, msg, nil, fields...)
}

// WarnWithError is Warn with an additional error argument that is reported to the error
// handler at WARN level
func (r *CompositeReporter) WarnWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	r.event(ctx, report.WarnLevel, msg, err, fields...)
}

// Error logs a message at ERROR level, reports the error to the error handler,
// and adds a trace event to the running span
func (r *CompositeReporter) Error(ctx context.Context, msg string, fields ...Fields) {
	r.event(ctx, report.ErrorLevel, msg, nil, fields...)
}

// ErrorWithError is Error with an additional error argument. If the
// error is actually a warning, reroutes to WarnWithError.
func (r *CompositeReporter) ErrorWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	r.event(ctx, report.ErrorLevel, msg, err, fields...)
}

// Panic logs a message at PANIC level, reports an error to rollbar, adds a
// trace event to the running span, and aborts the application with a panic
func (r *CompositeReporter) Panic(ctx context.Context, msg string, fields ...Fields) {
	r.event(ctx, report.PanicLevel, msg, nil, fields...)
}

// PanicWithError is Panic with an additional error argument
func (r *CompositeReporter) PanicWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	r.event(ctx, report.PanicLevel, msg, err, fields...)
}

// Fatal exits the application and should only be used in main(); consider
// returning errors or panics instead. Logs a message at FATAL level, reports an
// error to rollbar, adds a trace event to the running span, and exits the
// application.
func (r *CompositeReporter) Fatal(ctx context.Context, msg string, fields ...Fields) {
	r.event(ctx, report.FatalLevel, msg, nil, fields...)
}

// FatalWithError exits the application and should only be used in main();
// consider using returned errors or panics instead. Logs a message at FATAL
// level, reports an error to rollbar, adds a trace event to the running span,
// and exits the application.
func (r *CompositeReporter) FatalWithError(ctx context.Context, msg string, err error, fields ...Fields) {
	r.event(ctx, report.FatalLevel, msg, err, fields...)
}

// ReportRecoveredPanic is a convenience function for ensuring panics are
// reported to the reporter. Panics after reporting.
func (r *CompositeReporter) ReportRecoveredPanic(ctx context.Context) {
	if e := recover(); e != nil {
		r.Panic(ctx, fmt.Sprintf("panic caught: %+v", e))
	}
}

// Count tracks how many times something happened per second.
func (r *CompositeReporter) Count(ctx context.Context, name string, value int64, tags ...Fields) {
	r.logInternalError(ctx, r.metrics.Count(name, value, r.flattenWithBase(tags...)))
}

// Incr is just Count of 1.
func (r *CompositeReporter) Incr(ctx context.Context, name string, tags ...Fields) {
	r.logInternalError(ctx, r.metrics.Incr(name, r.flattenWithBase(tags...)))
}

// Decr is just Count of -1.
func (r *CompositeReporter) Decr(ctx context.Context, name string, tags ...Fields) {
	r.logInternalError(ctx, r.metrics.Decr(name, r.flattenWithBase(tags...)))
}

// Gauge measures the value of a metric at a particular time.
func (r *CompositeReporter) Gauge(ctx context.Context, name string, value float64, tags ...Fields) {
	r.logInternalError(ctx, r.metrics.Gauge(name, value, r.flattenWithBase(tags...)))
}

// Timing sends timing information in milliseconds.
func (r *CompositeReporter) Timing(ctx context.Context, name string, value time.Duration, tags ...Fields) {
	r.logInternalError(ctx, r.metrics.Timing(name, value, r.flattenWithBase(tags...)))
}

func (r *CompositeReporter) flattenWithBase(tags ...Fields) Fields {
	return flattenFields(append([]Fields{r.baseFields}, tags...)...)
}

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
func (r *CompositeReporter) StartSpan(ctx context.Context,
	spanName string, opts ...SpanOpt) (context.Context, SpanEndFunc) {

	start := time.Now()
	cfg := applyOpts(opts...)
	ctx, span := r.tracer.StartSpan(ctx, spanName, report.StartOpts{
		Kind:    report.SpanKind(cfg.kind),
		NewRoot: cfg.newRoot,
	})

	attr := mergeFields(r.baseFields, ctx)

	// Remove redundant otel fields from the span attributes (these will be injected in the log exporter)
	delete(attr, "span_id")
	delete(attr, "trace_id")
	span.SetAttrs(attr)

	return ctx, func(errs ...*error) {
		defer func() {
			tn, ta := span.TimingAttrs()

			if tn != "" {
				dur := time.Since(start)
				_ = r.metrics.Timing(tn, dur, ta)
			}
		}()
		if len(errs) == 0 {
			span.End()
			return
		}
		if len(errs) > 1 {
			r.Error(ctx, fmt.Sprintf("expecting 0 or 1 arguments to spanEnd function, got: %v", errs))
		}
		var actualErr error
		err := errs[0]
		if err != nil {
			actualErr = *err
		}
		r.AddResultToSpan(ctx, actualErr)
		span.End()
	}
}

// SetSpanAttribute retrieves a span from the given context and sets string attributes on the span.
func (r *CompositeReporter) SetSpanAttribute(ctx context.Context, k string, v any) {
	r.tracer.SpanFromContext(ctx).SetAttrs(map[string]any{k: v})
}

// SetSpanAttributes retrieves a span from the given context and sets multiple attributes on the span.
func (r *CompositeReporter) SetSpanAttributes(ctx context.Context, attrs ...Fields) {
	r.tracer.SpanFromContext(ctx).SetAttrs(flattenFields(attrs...))
}

// SetSpanStatusOk retrieves a span from the given context and sets a `Ok` status on the span.
func (r *CompositeReporter) SetSpanStatusOk(ctx context.Context, _ string) {
	r.tracer.SpanFromContext(ctx).SetStatusOk()
}

// SetSpanStatusError retrieves a span from the given context and sets a `Error` status on the span.
func (r *CompositeReporter) SetSpanStatusError(ctx context.Context, desc string) {
	r.tracer.SpanFromContext(ctx).SetStatusError(desc)
}

// MakeSpanGolden makes the current span in the ctx a golden span and returns the ctx containing the golden span
func (r *CompositeReporter) MakeSpanGolden(ctx context.Context) context.Context {
	return r.tracer.MakeSpanGolden(ctx)
}

const resultKey = "result"

// AddResultToSpan takes an error modeling the result of the span and
// uses it to set span status and several result-related fields:
//   - No err: SpanStatus=ok, result=success
//   - context canceled/deadline err: SpanStatus=unset, result=canceled, warning=err
//   - IsWarning err: SpanStatus=unset, result=warning, warning=err
//   - all other errs: SpanStatusError, result=error, error=err
//
// Prefer using StartSpan with an error argument if possible.
func (r *CompositeReporter) AddResultToSpan(ctx context.Context, err error) {
	// Set the semconv compliant things.
	// N.B. semconv suggests that libraries do NOT set a span status code to OK, instead leaving it up to
	// developers to explicitly set OK as they see fit, so we don't do anything in the nil err case
	switch {
	case IsWarning(err):
		// In the warning case always ask for the detail field to be set
		r.SetSpanAttributes(ctx, semconv.Error(err, true))
	case err != nil:
		desc := semconv.ErrorDescription(err, true)
		// Add in the other semconv error fields, including the details field if we are not setting a stats description.
		r.SetSpanAttributes(ctx, semconv.Error(err, desc == ""))
		r.SetSpanStatusError(ctx, desc)
	}

	// Add in the existing non semconv fields
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		r.SetSpanAttribute(ctx, resultKey, "canceled")
		r.SetSpanAttribute(ctx, "warning", err.Error())
	case IsWarning(err):
		r.SetSpanAttribute(ctx, resultKey, "warning")
		r.SetSpanAttribute(ctx, "warning", err.Error())
	case err != nil:
		r.SetSpanAttribute(ctx, resultKey, "error")
		r.SetSpanAttribute(ctx, "error", err.Error())
		r.ErrorWithError(ctx, "unexpected error", err)
	default:
		r.SetSpanAttribute(ctx, resultKey, "success")
	}
}

// TimingFromSpan sends a timing metric from the current span (when it ends), using name as the metric name
// with metric tags taken from the span attributes specified in attr. TimingFromSpan can be called any time
// before the span ends. If name is empty attr will be added to any current list.
func (r *CompositeReporter) TimingFromSpan(ctx context.Context, name string, attr ...string) {
	if name == "" {
		return
	}
	r.tracer.SpanFromContext(ctx).SetTimingAttrs(name, attr)
}

// AddTimingTags adds more span attribute names that will be added to any metric that it emitted from the span.
// This will only have an effect after a call to TimingFromSpan (within the same span context).
func (r *CompositeReporter) AddTimingTags(ctx context.Context, attr ...string) {
	r.tracer.SpanFromContext(ctx).SetTimingAttrs("", attr)
}

func (r *CompositeReporter) logInternalError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	r.log(ctx, report.ErrorLevel, err.Error())
}

// teardownBeforeFatal is tearDownExceptLogs but any error is logged, rather than returned
func (r *CompositeReporter) teardownBeforeFatal(ctx context.Context) {
	r.logInternalError(ctx, r.teardownExceptLogs(ctx))
}

// tearDownExceptLogs tears down stops the components in the reporter except logs
func (r *CompositeReporter) teardownExceptLogs(ctx context.Context) error {
	// Order is important; error handler stopped last for maximal observability into potential failures
	err := r.tracer.Shutdown(ctx)
	err = errors.Join(err, r.metrics.Close())
	return errors.Join(err, r.errorHandler.Close())
}

func (r *CompositeReporter) log(ctx context.Context, lvl report.Level, msg string, fields ...Fields) {
	r.logger.Log(lvl, msg, mergeFields(r.baseFields, ctx, fields...))
}

func (r *CompositeReporter) reportError(ctx context.Context,
	lvl report.Level, msg string, err error, redacted bool, fields ...Fields) {

	if err != nil {
		// If there was an error combine it with the message for reporting
		err = fmt.Errorf("%s: %w", msg, err)
	} else {
		// Warning events are reported to the error reporter
		err = errors.New(msg)
	}

	r.errorHandler.ReportError(lvl, err, mergeFields(r.baseFields, ctx, fields...))

	r.logInternalError(ctx, r.metrics.Incr("backplane.error-report", Fields{
		"level":    lvl.String(),
		"redacted": redacted,
	}))
}

func flattenWithError(err error, fields ...Fields) Fields {
	if err == nil {
		return flattenFields(fields...)
	}
	var extra Fields
	if reportAsWarning(err) {
		extra = warningFields(err)
	} else {
		extra = errorFields(err)
	}
	return flattenFields(append(fields, extra)...)
}

func (r *CompositeReporter) event(ctx context.Context, lvl report.Level, msg string, err error, fields ...Fields) {
	// Downgrade errors in the special warning error case
	if lvl == report.ErrorLevel && reportAsWarning(err) {
		lvl = report.WarnLevel
	}

	redacted, err := redactCircleToken(err)

	// Anything with warning and above or an error reports
	if lvl >= report.WarnLevel || err != nil {
		r.reportError(ctx, lvl, msg, err, redacted, fields...)
	}

	// Fatal kills the process so do some teardown
	if lvl == report.FatalLevel {
		r.teardownBeforeFatal(ctx)
	}

	if r.eventHook != nil {
		r.eventHook(lvl, msg, err, mergeFields(r.baseFields, ctx, fields...))
	}

	// All events produce a log.
	// N.B. log must be last as some levels cause a panic or exit
	r.log(ctx, lvl, msg, flattenWithError(err, fields...))
}
