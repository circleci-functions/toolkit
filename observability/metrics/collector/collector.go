// Package collector implements a metrics collector that collects
// standard metrics from the application and reports them using an
// observability.Reporter.
package collector

import (
	"context"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	o11y "github.com/circleci-functions/toolkit/observability"
)

// MetricsCollector collects standard metrics from the application and reports
// them using an observability.Reporter
type MetricsCollector struct {
	collectOnce               sync.Once
	lastSeenCumulativeGCPause uint64
}

// New returns a new MetricsCollector
func New() *MetricsCollector {
	return &MetricsCollector{}
}

// Run starts the MetricsCollector
func (m *MetricsCollector) Run(ctx context.Context) error {
	m.collectSystemMetrics(ctx)
	return nil
}

// Teardown stops the MetricsCollector
func (m *MetricsCollector) Teardown(_ context.Context) error {
	return nil
}

func (m *MetricsCollector) sendMetrics(ctx context.Context, ms *runtime.MemStats, gc *debug.GCStats) {
	runtime.ReadMemStats(ms)
	debug.ReadGCStats(gc)

	// CPU statistics
	o11y.Gauge(ctx, "backplane.system.goroutines", float64(runtime.NumGoroutine()))
	o11y.Gauge(ctx, "backplane.system.cgo_calls", float64(runtime.NumCgoCall()))
	o11y.Gauge(ctx, "backplane.system.num_cpu", float64(runtime.NumCPU()))
	// General statistics
	o11y.Gauge(ctx, "backplane.system.mem_stats.alloc", float64(ms.Alloc))
	o11y.Gauge(ctx, "backplane.system.mem_stats.total_alloc", float64(ms.TotalAlloc))
	o11y.Gauge(ctx, "backplane.system.mem_stats.sys", float64(ms.Sys))
	o11y.Gauge(ctx, "backplane.system.mem_stats.lookups", float64(ms.Lookups))
	o11y.Gauge(ctx, "backplane.system.mem_stats.mallocs", float64(ms.Mallocs))
	o11y.Gauge(ctx, "backplane.system.mem_stats.frees", float64(ms.Frees))
	// Heap memory statistics
	o11y.Gauge(ctx, "backplane.system.mem_stats.heap_alloc", float64(ms.HeapAlloc))
	o11y.Gauge(ctx, "backplane.system.mem_stats.heap_sys", float64(ms.HeapSys))
	o11y.Gauge(ctx, "backplane.system.mem_stats.heap_idle", float64(ms.HeapIdle))
	o11y.Gauge(ctx, "backplane.system.mem_stats.heap_inuse", float64(ms.HeapInuse))
	o11y.Gauge(ctx, "backplane.system.mem_stats.heap_released", float64(ms.HeapReleased))
	o11y.Gauge(ctx, "backplane.system.mem_stats.heap_objects", float64(ms.HeapObjects))
	// Stack memory statistics
	o11y.Gauge(ctx, "backplane.system.mem_stats.stack_inuse", float64(ms.StackInuse))
	o11y.Gauge(ctx, "backplane.system.mem_stats.stack_sys", float64(ms.StackSys))
	// Off-heap memory statistics
	o11y.Gauge(ctx, "backplane.system.mem_stats.m_span_inuse", float64(ms.MSpanInuse))
	o11y.Gauge(ctx, "backplane.system.mem_stats.m_span_sys", float64(ms.MSpanSys))
	o11y.Gauge(ctx, "backplane.system.mem_stats.m_cache_inuse", float64(ms.MCacheInuse))
	o11y.Gauge(ctx, "backplane.system.mem_stats.m_cache_sys", float64(ms.MCacheSys))
	o11y.Gauge(ctx, "backplane.system.mem_stats.buck_hash_sys", float64(ms.BuckHashSys))
	o11y.Gauge(ctx, "backplane.system.mem_stats.gc_sys", float64(ms.GCSys))
	o11y.Gauge(ctx, "backplane.system.mem_stats.other_sys", float64(ms.OtherSys))
	// Garbage collector statistics
	o11y.Gauge(ctx, "backplane.system.mem_stats.next_gc", float64(ms.NextGC))
	o11y.Gauge(ctx, "backplane.system.mem_stats.last_gc", float64(ms.LastGC))
	o11y.Gauge(ctx, "backplane.system.mem_stats.pause_total_ns", float64(ms.PauseTotalNs))
	o11y.Gauge(ctx, "backplane.system.mem_stats.num_gc", float64(ms.NumGC))
	o11y.Gauge(ctx, "backplane.system.mem_stats.num_forced_gc", float64(ms.NumForcedGC))
	o11y.Gauge(ctx, "backplane.system.mem_stats.gc_cpu_fraction", ms.GCCPUFraction)
	for i, p := range []string{"min", "25p", "50p", "75p", "max"} {
		o11y.Gauge(ctx, "backplane.system.gc_stats.pause_quantiles."+p, float64(gc.PauseQuantiles[i]))
	}

	gcPauseSinceLastRecording := ms.PauseTotalNs - m.lastSeenCumulativeGCPause
	m.lastSeenCumulativeGCPause = ms.PauseTotalNs
	o11y.Gauge(ctx, "backplane.system.mem_stats.pause_iterative_ns", float64(gcPauseSinceLastRecording))
}

// collectSystemMetrics is a blocking method that collects various system
// metrics not already reported by other means.
// This method should only called once. Any extra call would result in a
// non-blocking no-op
func (m *MetricsCollector) collectSystemMetrics(ctx context.Context) {
	m.collectOnce.Do(func() {
		var ms runtime.MemStats
		gc := debug.GCStats{
			// When len(stats.PauseQuantiles) is 5, it will be filled with the
			// minimum, 25%, 50%, 75%, and maximum pause times. See the documentation
			// for (runtime/debug).ReadGCStats.
			PauseQuantiles: make([]time.Duration, 5),
		}

		m.sendMetrics(ctx, &ms, &gc)

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sendMetrics(ctx, &ms, &gc)
			}
		}
	})
}
