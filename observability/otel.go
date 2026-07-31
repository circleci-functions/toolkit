package observability

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/circleci-functions/toolkit/observability/otel"
)

// OTELTracerProvider gets the underlying OTEL trace.TracerProvider from the context.
//
// This function should *NOT* be used to directly add instrumentation to your codebase.
//
// It should only be used to support integrating third-party OTEL instrumentation from places
// like: https://github.com/open-telemetry/opentelemetry-go-contrib/tree/main/instrumentation
func OTELTracerProvider(ctx context.Context) trace.TracerProvider {
	rep := FromContext(ctx)
	cr, ok := rep.(*CompositeReporter)
	if !ok {
		return nil
	}

	tp, ok := cr.tracer.(*otel.TracerProvider)
	if !ok {
		return nil
	}

	return tp.TracerProvider()
}
