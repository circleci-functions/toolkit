package observability

import (
	"context"
)

// FromContext extracts a Reporter from the given context.
// Returns a no-op Reporter if there is none present.
func FromContext(ctx context.Context) Reporter {
	r, ok := ctx.Value(reporterKey{}).(Reporter)
	if !ok {
		return NewCompositeReporter()
	}
	return r
}

// WithReporter returns a new context that includes the given Reporter.
func WithReporter(ctx context.Context, rep Reporter) context.Context {
	return context.WithValue(ctx, reporterKey{}, rep)
}

// HasReporter returns true if there is a reporter in the context.
func HasReporter(ctx context.Context) bool {
	_, ok := ctx.Value(reporterKey{}).(Reporter)
	return ok
}

type reporterKey struct{}
