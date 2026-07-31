/*
Package testcontext provides a context with a reporter wired in, setup for logging and no-op
metrics.

TL;DR - will give your tests pretty logs!
*/
package testcontext

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	o11y "github.com/circleci-functions/toolkit/observability"
)

var ctx = newContext()

// Background returns a context for use in tests which contains a working reporter, so you get logs, etc.
func Background() context.Context {
	return ctx
}

func newContext() context.Context {
	return o11y.WithReporter(context.Background(), o11y.Simple(context.Background()))
}

// New returns a Background() context that tears down the reporter
// (flushes the telemetry) as your test stops
func New(t *testing.T) context.Context {
	t.Helper()

	ctx := newContext()
	t.Cleanup(func() {
		err := o11y.FromContext(ctx).Teardown(ctx)
		assert.Assert(t, err)
	})
	return ctx
}
