package observability

import (
	"context"
	"time"
)

// Count delegates to #Reporter.Count
func Count(ctx context.Context, name string, value int64, tags ...Fields) {
	FromContext(ctx).Count(ctx, name, value, tags...)
}

// Incr delegates to #Reporter.Incr
func Incr(ctx context.Context, name string, tags ...Fields) {
	FromContext(ctx).Incr(ctx, name, tags...)
}

// Decr delegates to #Reporter.Decr
func Decr(ctx context.Context, name string, tags ...Fields) {
	FromContext(ctx).Decr(ctx, name, tags...)
}

// Gauge delegates to #Reporter.Gauge
func Gauge(ctx context.Context, name string, value float64, tags ...Fields) {
	FromContext(ctx).Gauge(ctx, name, value, tags...)
}

// Timing delegates to #Reporter.Timing
func Timing(ctx context.Context, name string, value time.Duration, tags ...Fields) {
	FromContext(ctx).Timing(ctx, name, value, tags...)
}
