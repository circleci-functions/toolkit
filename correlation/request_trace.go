package correlation

import (
	"context"

	"github.com/google/uuid"
)

type (
	requestIDKey     struct{}
	clientTraceIDKey struct{}
)

func getID(ctx context.Context, key any) string {
	val := ctx.Value(key)
	if val != nil {
		return val.(string)
	}
	return ""
}

// GetRequestID returns the request ID associated with a context. Each HTTP
// request is assigned a unique request ID for logging and auditing.
//
// Libraries should propagate and emit this ID automatically, so applications
// should not use this function directly.
func GetRequestID(ctx context.Context) string {
	return getID(ctx, requestIDKey{})
}

// WithRequestID stores a request ID in a context.
//
// Libraries should propagate and emit this ID automatically, so applications
// should not use this function directly.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// GenerateRequestID creates a new request ID
//
// Libraries should propagate and emit this ID automatically, so applications
// should not use this function directly.
func GenerateRequestID() string {
	return uuid.NewString()
}

// GetClientTraceID returns the client trace ID associated with a
// context.
//
// Libraries should propagate and emit this ID automatically, so applications
// should not use this function directly.
func GetClientTraceID(ctx context.Context) string {
	return getID(ctx, clientTraceIDKey{})
}

// WithClientTraceID stores a client trace ID in a context.
//
// Libraries should propagate and emit this ID automatically, so applications
// should not use this function directly.
func WithClientTraceID(ctx context.Context, clientTraceID string) context.Context {
	return context.WithValue(ctx, clientTraceIDKey{}, clientTraceID)
}
