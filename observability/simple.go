package observability

import (
	"context"
)

// Simple creates a new reporter that emits to standard output only.
func Simple(ctx context.Context) Reporter {
	reporter, err := NewReporterFromConfig(ctx, ReporterConfig{
		LogDestination:         "stderr",
		LogLevel:               "debug",
		LogFormat:              "color",
		ErrorReportDestination: "stderr",
		ErrorReportLevel:       "error",
		TraceDestinations:      []string{"log"},
		MetricDestination:      "none",
	})
	if err != nil {
		panic(err)
	}
	return reporter
}
