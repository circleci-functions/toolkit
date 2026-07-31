package report

import "time"

type MetricEmitter interface {
	Count(name string, value int64, tags map[string]any) error
	Incr(name string, tags map[string]any) error
	Decr(name string, tags map[string]any) error
	Gauge(name string, value float64, tags map[string]any) error
	Timing(name string, value time.Duration, tags map[string]any) error
	Close() error
}
