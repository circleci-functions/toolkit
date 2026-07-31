package observability

import (
	"context"
	"encoding/json"
	"maps"

	"github.com/circleci-functions/toolkit/correlation"
)

// Fields contains key value pairs of context to add to observability
// data, e.g. project_id: 123
type Fields map[string]any

func (f Fields) String() string {
	if f == nil {
		return ""
	}
	b, _ := json.Marshal(f)
	return string(b)
}

// AsMap is a way to not have the 'otel' package know about Fields (to avoid an import cycle)
// it is something of a hack whilst we automatically build the internal info field on the cut over to a better
// controlled tracing system.
func (f Fields) AsMap() map[string]any {
	return f
}

// WithFields returns a new context with additional observability fields, such
// that telemetry data emitted using the returned context automatically
// includes those fields.
// Fields added using WithFields are not included on any emitted metrics since
// they typically carry high cardinality values.
func WithFields(ctx context.Context, fields Fields) context.Context {
	if len(fields) == 0 {
		return ctx
	}
	newFields := Fields{}
	for k, v := range getFields(ctx) {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return context.WithValue(ctx, fieldsKey{}, newFields)
}

type fieldsKey struct{}

func getFields(ctx context.Context) Fields {
	if val, ok := ctx.Value(fieldsKey{}).(Fields); ok {
		return val
	}
	return Fields{}
}

// flattenFields is a utility that flattens a variable-arity Fields
// array into a single Fields map
func flattenFields(fields ...Fields) Fields {
	flat := Fields{}

	for _, f := range fields {
		for k, v := range f {
			flat[k] = v
		}
	}
	return flat
}

// warningFields returns Fields with the standardized way to represent
// a warning: the error message in the "warning" field
func warningFields(err error) Fields {
	return Fields{
		"warning": err.Error(),
	}
}

// errorFields returns Fields with the standardized way to represent
// an unexpected error: the error message in the "error" field
func errorFields(err error) Fields {
	return Fields{
		"error": err.Error(),
	}
}

// MergeFields merges fields by precedence
//
// In order of least to most precedent:
// Base fields (from env) < Context < Additional fields < Internal field
func mergeFields(base Fields, ctx context.Context, additional ...Fields) Fields { //nolint:revive // ok that ctx is not first arg
	combined := Fields{}
	for k, v := range base {
		combined[k] = v
	}
	for k, v := range getFields(ctx) {
		combined[k] = copyIfMap(v)
	}
	for _, fieldMap := range additional {
		for k, v := range fieldMap {
			combined[k] = v
		}
	}
	if loggerName, has := getLoggerName(ctx); has {
		combined["logger_name"] = loggerName
	}
	ids := correlation.FromContext(ctx)
	if ids.TraceID != "" {
		combined["trace_id"] = ids.TraceID
	}
	if ids.SpanID != "" {
		combined["span_id"] = ids.SpanID
	}
	if ids.RequestID != "" {
		combined["request_id"] = ids.RequestID
	}
	if ids.ClientTraceID != "" {
		combined["client_trace_id"] = ids.ClientTraceID
	}
	return combined
}

func copyIfMap(m any) any {
	mm, ok := m.(map[string]any)
	if !ok {
		return m
	}
	return maps.Clone(mm)
}
