package observability

import (
	"context"
)

// StartSpan delegates to #Reporter.StartSpan
func StartSpan(ctx context.Context, spanName string, opts ...SpanOpt) (context.Context, SpanEndFunc) {
	return FromContext(ctx).StartSpan(ctx, spanName, opts...)
}

// SetSpanAttribute delegates to #Reporter.SetSpanAttribute
func SetSpanAttribute(ctx context.Context, k string, v any) {
	FromContext(ctx).SetSpanAttribute(ctx, k, v)
}

// SetSpanAttributes delegates to #Reporter.SetSpanAttributes
func SetSpanAttributes(ctx context.Context, attrs ...Fields) {
	FromContext(ctx).SetSpanAttributes(ctx, attrs...)
}

// SetSpanStatusOk delegates to #Reporter.SetSpanStatusOk
func SetSpanStatusOk(ctx context.Context, desc string) {
	FromContext(ctx).SetSpanStatusOk(ctx, desc)
}

// SetSpanStatusError delegates to #Reporter.SetSpanStatusError
func SetSpanStatusError(ctx context.Context, desc string) {
	FromContext(ctx).SetSpanStatusError(ctx, desc)
}

// AddResultToSpan delegates to #Reporter.AddResultToSpan
func AddResultToSpan(ctx context.Context, err error) {
	FromContext(ctx).AddResultToSpan(ctx, err)
}

// TimingFromSpan sends a timing metric from the current span (when it ends), using name as the metric name
// and attr as the list of fields to use from the span as metric tags. TimingFromSpan can be called any time
// before the span ends. Subsequent calls the context of the same span will have no effect - use
// AddTimingTags to add more tags.
func TimingFromSpan(ctx context.Context, name string, attrs ...string) {
	FromContext(ctx).TimingFromSpan(ctx, name, attrs...)
}

// AddTimingTags adds more span attribute names that will be added to any metric that is emitted from the span.
// This will only have an effect after a call to TimingFromSpan (within the same span context).
func AddTimingTags(ctx context.Context, attrs ...string) {
	FromContext(ctx).AddTimingTags(ctx, attrs...)
}

// SetSpanSampledIn causes this span to be sampled in, even if the overall trace is sampled out.
func SetSpanSampledIn(ctx context.Context) {
	// This 'magic' attribute is used by the open telemetry collector to ensure that the span is never sampled out.
	FromContext(ctx).SetSpanAttribute(ctx, "meta.keep.span", true)
}
