package otel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/circleci-functions/toolkit/internal/report"
)

type exporter struct {
	emitter report.SpanEmitter

	stoppedMu sync.RWMutex
	stopped   bool
}

var _ sdktrace.SpanExporter = (*exporter)(nil)

func (e *exporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.stoppedMu.RLock()
	stopped := e.stopped
	e.stoppedMu.RUnlock()
	if stopped {
		return nil
	}

	for _, s := range spans {
		e.exportSpan(s)
	}

	return nil
}

func (e *exporter) exportSpan(s sdktrace.ReadOnlySpan) {
	var parentID string
	if s.Parent().HasSpanID() {
		parentID = s.Parent().SpanID().String()
	}
	var status report.SpanStatus
	switch s.Status().Code {
	case codes.Ok:
		status = report.OkStatus
	case codes.Error:
		status = report.ErrorStatus
	case codes.Unset:
		status = report.UnsetStatus
	}
	rspan := report.RSpan{
		Name:         s.Name(),
		Kind:         report.SpanKind(s.SpanKind()),
		ParentSpanID: parentID,
		SpanID:       s.SpanContext().SpanID().String(),
		TraceID:      s.SpanContext().TraceID().String(),
		StartTime:    s.StartTime(),
		EndTime:      s.EndTime(),
		Attrs:        attributesToMap(s.Attributes()),
		Status:       status,
		StatusDesc:   s.Status().Description,
	}
	e.emitter.ExportSpan(rspan)
}

func (e *exporter) Shutdown(ctx context.Context) error {
	e.stoppedMu.Lock()
	e.stopped = true
	e.stoppedMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

func attributesToMap(attributes []attribute.KeyValue) map[string]any {
	m := make(map[string]any, len(attributes))
	for _, a := range attributes {
		m[string(a.Key)] = a.Value.AsInterface()
	}
	return m
}
