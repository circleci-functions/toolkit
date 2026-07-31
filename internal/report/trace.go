package report

import (
	"context"
	"encoding/json"
	"time"

	"github.com/circleci-functions/toolkit/observability/attr"
)

// SpanKind is the role a Span plays in a Trace.
type SpanKind int

// These are direct copies of the otel values - see them for documentation
const (
	SpanKindInternal SpanKind = 1
	SpanKindServer   SpanKind = 2
	SpanKindClient   SpanKind = 3
	SpanKindProducer SpanKind = 4
	SpanKindConsumer SpanKind = 5
)

type StartOpts struct {
	Kind    SpanKind
	NewRoot bool
}

func DefaultStartOpts() StartOpts {
	return StartOpts{
		Kind:    SpanKindInternal,
		NewRoot: false,
	}
}

type TraceEmitter interface {
	StartSpan(ctx context.Context, spanName string, opts StartOpts) (context.Context, WSpan)
	SpanFromContext(ctx context.Context) WSpan
	Shutdown(ctx context.Context) error
	MakeSpanGolden(ctx context.Context) context.Context
}

type WSpan interface {
	SetAttrs(attrs map[string]any)
	SetStatusOk()
	SetStatusError(desc string)
	End()
	SetTimingAttrs(name string, attrs []string)
	TimingAttrs() (string, map[string]any)
}

// RSpan are the fields that we use in SpanEmitter
type RSpan struct {
	Name         string
	Kind         SpanKind
	SpanID       string
	TraceID      string
	ParentSpanID string
	StartTime    time.Time
	EndTime      time.Time
	Attrs        map[string]any
	Status       SpanStatus
	StatusDesc   string
}

type SpanStatus string

const (
	OkStatus    SpanStatus = "ok"
	ErrorStatus SpanStatus = "error"
	UnsetStatus SpanStatus = ""
)

type SpanEmitter interface {
	ExportSpan(span RSpan)
}

// LogToSpan lets you use a LogEmitter as a SpanEmitter
type LogToSpan struct {
	LogEmitter interface {
		Log(level Level, msg string, fields map[string]any)
	}
}

var _ SpanEmitter = (*LogToSpan)(nil)

func (l *LogToSpan) ExportSpan(span RSpan) {
	attrs := make(map[string]any, len(span.Attrs))
	for k, v := range span.Attrs {
		// Avoid the info field duplicating any existing attributes during the transition to environments.
		// This is intended to keep any explicit additions to the info field, and remove any that were automatically
		// added, and any that were accidentally duplicated. This is intended to smooth the transition
		// environments.
		if k == attr.InternalInfo {
			vs, ok := v.(string)
			if ok {
				w, use := dedupeInfo(vs, span.Attrs)
				if use {
					v = w
				} else {
					continue
				}
			}
		}
		attrs[k] = v
	}

	if span.SpanID != "" {
		attrs["span_id"] = span.SpanID
	}
	if span.TraceID != "" {
		attrs["trace_id"] = span.TraceID
	}
	if span.ParentSpanID != "" {
		attrs["parent_span_id"] = span.ParentSpanID
	}

	duration := float64(span.EndTime.Sub(span.StartTime).Microseconds()) / 1000.0
	attrs["duration_ms"] = duration

	if span.StatusDesc != "" {
		attrs["status_desc"] = span.StatusDesc
	}

	attrs["status_code"] = span.Status
	attrs["span_kind"] = KindToString(span.Kind)

	level := InfoLevel
	if _, ok := attrs["warning"].(string); ok {
		level = WarnLevel
	}
	if e, ok := attrs["error"].(string); ok {
		if e != "" && e != "<nil>" {
			level = ErrorLevel
		}
	}
	l.LogEmitter.Log(level, span.Name, attrs)
}

// dedupeInfo - removes others fields from the json serialized info string.
// The un-marshaling and marshaling is unlikely to error, if it does the info string is returned intact.
// Returns true if field should be retained.
func dedupeInfo(info string, others map[string]any) (string, bool) {
	m := make(map[string]any)
	if err := json.Unmarshal([]byte(info), &m); err != nil {
		return info, true
	}
	for k := range others {
		delete(m, k)
	}
	if len(m) == 0 {
		return "", false
	}
	b, err := json.Marshal(m)
	if err != nil {
		return info, true
	}
	return string(b), true
}

func KindToString(k SpanKind) string {
	switch k {
	case SpanKindInternal:
		return "internal"
	case SpanKindClient:
		return "client"
	case SpanKindServer:
		return "server"
	case SpanKindProducer:
		return "producer"
	case SpanKindConsumer:
		return "consumer"
	default:
		return "internal"
	}
}
