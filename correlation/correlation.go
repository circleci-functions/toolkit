// Package correlation contains helpers to get known Correlation IDs that are
// passed between services and stored in the Context. Libraries should propagate
// and emit these IDs automatically, so applications shouldn't need to use this
// package directly.
//
// Currently supports OpenTelemetry trace and span identifiers and the request
// and client trace identifiers from the Clojure request-trace library
package correlation

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// IDs is a struct of known correlation IDs
type IDs struct {
	// TraceID is the OpenTelemetry trace ID
	TraceID string
	// SpanID is the OpenTelemetry span ID
	SpanID string

	// RequestID is the unique ID assigned to every HTTP request for logging and
	// auditing. Prefer using TraceID unless RequestID is specifically needed.
	RequestID string
	// ClientTraceID is a unique client-assigned ID. Clients may specify a trace
	// ID that can be used for logging and debugging. The trace ID behaves
	// similarly to the request ID, except it may be set to a known value by the
	// client when debugging. There is no guarantee that the trace ID is unique,
	// since it is set by the client.
	ClientTraceID string
}

// FromContext returns all known correlation IDs from the context. Libraries
// should propagate and emit these IDs automatically, so applications shouldn't
// need to use this function directly.
func FromContext(ctx context.Context) IDs {
	ids := IDs{}
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.HasTraceID() {
		ids.TraceID = spanContext.TraceID().String()
	}
	if spanContext.HasSpanID() {
		ids.SpanID = spanContext.SpanID().String()
	}
	ids.RequestID = GetRequestID(ctx)
	ids.ClientTraceID = GetClientTraceID(ctx)
	return ids
}
