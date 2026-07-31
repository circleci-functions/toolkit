// Package noop is an emitter that emits nothing
//
//nolint:revive // noop
package noop

import (
	"context"
	"os"
	"time"

	"github.com/circleci-functions/toolkit/internal/report"
)

var Emitter = &noOp{}

type noOp struct{}

var (
	_ report.ErrorEmitter  = (*noOp)(nil)
	_ report.MetricEmitter = (*noOp)(nil)
	_ report.TraceEmitter  = (*noOp)(nil)
)

func (n *noOp) Log(level report.Level, msg string, _ map[string]any) {
	if level == report.PanicLevel {
		panic(msg)
	}
	if level == report.FatalLevel {
		os.Exit(1)
	}
}

func (n *noOp) Teardown(context.Context) error { return nil }

func (n *noOp) SendDeployment()                                                 {}
func (n *noOp) ReportError(level report.Level, err error, extra map[string]any) {}
func (n *noOp) Close() error                                                    { return nil }

func (n *noOp) Count(name string, value int64, tags map[string]any) error          { return nil }
func (n *noOp) Incr(name string, tags map[string]any) error                        { return nil }
func (n *noOp) Decr(name string, tags map[string]any) error                        { return nil }
func (n *noOp) Gauge(name string, value float64, tags map[string]any) error        { return nil }
func (n *noOp) Timing(name string, value time.Duration, tags map[string]any) error { return nil }

func (n *noOp) StartSpan(ctx context.Context, _ string, _ report.StartOpts) (context.Context, report.WSpan) {
	return ctx, &noOpSpan{}
}
func (n *noOp) SpanFromContext(ctx context.Context) report.WSpan   { return &noOpSpan{} }
func (n *noOp) MakeSpanGolden(ctx context.Context) context.Context { return ctx }

func (n *noOp) Shutdown(ctx context.Context) error {
	return nil
}

type noOpSpan struct{}

func (n *noOpSpan) SetAttrs(attrs map[string]any)         {}
func (n *noOpSpan) SetStatusOk()                          {}
func (n *noOpSpan) SetStatusError(desc string)            {}
func (n *noOpSpan) End()                                  {}
func (n *noOpSpan) SetTimingAttrs(_ string, _ []string)   {}
func (n *noOpSpan) TimingAttrs() (string, map[string]any) { return "", nil }
