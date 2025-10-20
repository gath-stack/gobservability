package metrics

import (
	"context"
	"runtime"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// RuntimeMetrics provides a comprehensive set of metrics that describe the Go
// process state in real time.
//
// It includes CPU user/system time, memory allocation statistics, garbage
// collection behavior, number of active goroutines, system threads, and cgo
// call counts. Metrics are gathered using OpenTelemetry asynchronous instruments
// and reported automatically through a registered callback.
//
// This type is thread-safe and optimized for low-overhead collection.
type RuntimeMetrics struct {
	// CPU Metrics
	cpuUser   metric.Float64ObservableCounter
	cpuSystem metric.Float64ObservableCounter

	// Memory metrics
	heapAlloc    metric.Int64ObservableGauge
	heapSys      metric.Int64ObservableGauge
	heapIdle     metric.Int64ObservableGauge
	heapInuse    metric.Int64ObservableGauge
	heapReleased metric.Int64ObservableGauge
	heapObjects  metric.Int64ObservableGauge
	stackInuse   metric.Int64ObservableGauge
	stackSys     metric.Int64ObservableGauge
	totalAlloc   metric.Int64ObservableCounter
	mallocs      metric.Int64ObservableCounter
	frees        metric.Int64ObservableCounter

	// GC metrics
	gcCount       metric.Int64ObservableCounter
	gcPauseTotal  metric.Int64ObservableCounter
	gcPauseLast   metric.Int64ObservableGauge
	gcCPUFraction metric.Float64ObservableGauge
	nextGC        metric.Int64ObservableGauge

	// Goroutines and threads
	goroutines metric.Int64ObservableGauge
	threads    metric.Int64ObservableGauge
	cgoCalls   metric.Int64ObservableCounter

	// Internal state for tracking
	lastNumGC      uint32
	lastPauseNs    uint64
	lastTotalAlloc uint64
	lastMallocs    uint64
	lastFrees      uint64
	lastCgoCalls   int64
}

// NewRuntimeMetrics creates and registers all standard runtime metrics
// for the current Go process using the provided OpenTelemetry Meter.
//
// Metrics include CPU (user/system), heap memory, stack usage, garbage
// collection, goroutines, threads, and cgo calls.
//
// The function registers an internal callback to collect metrics periodically.
// Once created, no further manual updates are required.
//
// Returns an initialized *RuntimeMetrics or an error if any metric instrument
// fails to be registered.
func NewRuntimeMetrics(meter metric.Meter) (*RuntimeMetrics, error) {
	rm := &RuntimeMetrics{}

	var err error

	// Cpu Metrics
	rm.cpuUser, err = meter.Float64ObservableCounter(
		"go.cpu.user",
		metric.WithDescription("User CPU time used by the Go process in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	rm.cpuSystem, err = meter.Float64ObservableCounter(
		"go.cpu.system",
		metric.WithDescription("System CPU time used by the Go process in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	// Memory metrics
	rm.heapAlloc, err = meter.Int64ObservableGauge(
		"go.memory.heap.alloc",
		metric.WithDescription("Bytes of allocated heap objects"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.heapSys, err = meter.Int64ObservableGauge(
		"go.memory.heap.sys",
		metric.WithDescription("Total bytes obtained from OS for heap"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.heapIdle, err = meter.Int64ObservableGauge(
		"go.memory.heap.idle",
		metric.WithDescription("Bytes in idle spans"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.heapInuse, err = meter.Int64ObservableGauge(
		"go.memory.heap.inuse",
		metric.WithDescription("Bytes in in-use spans"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.heapReleased, err = meter.Int64ObservableGauge(
		"go.memory.heap.released",
		metric.WithDescription("Bytes of physical memory returned to OS"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.heapObjects, err = meter.Int64ObservableGauge(
		"go.memory.heap.objects",
		metric.WithDescription("Number of allocated heap objects"),
		metric.WithUnit("{object}"),
	)
	if err != nil {
		return nil, err
	}

	rm.stackInuse, err = meter.Int64ObservableGauge(
		"go.memory.stack.inuse",
		metric.WithDescription("Bytes in stack spans"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.stackSys, err = meter.Int64ObservableGauge(
		"go.memory.stack.sys",
		metric.WithDescription("Total bytes obtained from OS for stack"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.totalAlloc, err = meter.Int64ObservableCounter(
		"go.memory.alloc.total",
		metric.WithDescription("Cumulative bytes allocated for heap objects"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	rm.mallocs, err = meter.Int64ObservableCounter(
		"go.memory.mallocs",
		metric.WithDescription("Cumulative count of heap objects allocated"),
		metric.WithUnit("{allocation}"),
	)
	if err != nil {
		return nil, err
	}

	rm.frees, err = meter.Int64ObservableCounter(
		"go.memory.frees",
		metric.WithDescription("Cumulative count of heap objects freed"),
		metric.WithUnit("{free}"),
	)
	if err != nil {
		return nil, err
	}

	// GC metrics
	rm.gcCount, err = meter.Int64ObservableCounter(
		"go.gc.count",
		metric.WithDescription("Number of completed GC cycles"),
		metric.WithUnit("{gc}"),
	)
	if err != nil {
		return nil, err
	}

	rm.gcPauseTotal, err = meter.Int64ObservableCounter(
		"go.gc.pause.total",
		metric.WithDescription("Cumulative nanoseconds in GC stop-the-world pauses"),
		metric.WithUnit("ns"),
	)
	if err != nil {
		return nil, err
	}

	rm.gcPauseLast, err = meter.Int64ObservableGauge(
		"go.gc.pause.last",
		metric.WithDescription("Last GC pause duration in nanoseconds"),
		metric.WithUnit("ns"),
	)
	if err != nil {
		return nil, err
	}

	rm.gcCPUFraction, err = meter.Float64ObservableGauge(
		"go.gc.cpu.fraction",
		metric.WithDescription("Fraction of CPU time used by GC"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	rm.nextGC, err = meter.Int64ObservableGauge(
		"go.gc.heap.goal",
		metric.WithDescription("Target heap size for next GC"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	// Goroutines and threads
	rm.goroutines, err = meter.Int64ObservableGauge(
		"go.goroutines",
		metric.WithDescription("Number of goroutines that currently exist"),
		metric.WithUnit("{goroutine}"),
	)
	if err != nil {
		return nil, err
	}

	rm.threads, err = meter.Int64ObservableGauge(
		"go.threads",
		metric.WithDescription("Number of OS threads created"),
		metric.WithUnit("{thread}"),
	)
	if err != nil {
		return nil, err
	}

	rm.cgoCalls, err = meter.Int64ObservableCounter(
		"go.cgo.calls",
		metric.WithDescription("Number of cgo calls made"),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, err
	}

	// Register callback per aggiornare tutte le metriche
	_, err = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			rm.collect(o)
			return nil
		},
		rm.cpuUser,
		rm.cpuSystem,
		rm.heapAlloc,
		rm.heapSys,
		rm.heapIdle,
		rm.heapInuse,
		rm.heapReleased,
		rm.heapObjects,
		rm.stackInuse,
		rm.stackSys,
		rm.totalAlloc,
		rm.mallocs,
		rm.frees,
		rm.gcCount,
		rm.gcPauseTotal,
		rm.gcPauseLast,
		rm.gcCPUFraction,
		rm.nextGC,
		rm.goroutines,
		rm.threads,
		rm.cgoCalls,
	)
	if err != nil {
		return nil, err
	}

	return rm, nil
}

// collect gathers the latest runtime statistics and records them into the
// provided OpenTelemetry Observer.
//
// It computes deltas for cumulative metrics (e.g., total allocations, frees,
// GC cycles) and instantaneous values for gauges (e.g., heap in use, goroutines).
//
// This method is automatically invoked by the Meter’s registered callback and
// should not be called directly.
func (rm *RuntimeMetrics) collect(o metric.Observer) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Memory metrics
	o.ObserveInt64(rm.heapAlloc, int64(m.HeapAlloc))
	o.ObserveInt64(rm.heapSys, int64(m.HeapSys))
	o.ObserveInt64(rm.heapIdle, int64(m.HeapIdle))
	o.ObserveInt64(rm.heapInuse, int64(m.HeapInuse))
	o.ObserveInt64(rm.heapReleased, int64(m.HeapReleased))
	o.ObserveInt64(rm.heapObjects, int64(m.HeapObjects))
	o.ObserveInt64(rm.stackInuse, int64(m.StackInuse))
	o.ObserveInt64(rm.stackSys, int64(m.StackSys))

	// Cumulative counters - report delta
	if rm.lastTotalAlloc > 0 {
		o.ObserveInt64(rm.totalAlloc, int64(m.TotalAlloc-rm.lastTotalAlloc))
	}
	rm.lastTotalAlloc = m.TotalAlloc

	if rm.lastMallocs > 0 {
		o.ObserveInt64(rm.mallocs, int64(m.Mallocs-rm.lastMallocs))
	}
	rm.lastMallocs = m.Mallocs

	if rm.lastFrees > 0 {
		o.ObserveInt64(rm.frees, int64(m.Frees-rm.lastFrees))
	}
	rm.lastFrees = m.Frees

	// GC metrics
	if rm.lastNumGC > 0 {
		o.ObserveInt64(rm.gcCount, int64(m.NumGC-rm.lastNumGC))
	}
	rm.lastNumGC = m.NumGC

	if rm.lastPauseNs > 0 {
		o.ObserveInt64(rm.gcPauseTotal, int64(m.PauseTotalNs-rm.lastPauseNs))
	}
	rm.lastPauseNs = m.PauseTotalNs

	// Last GC pause
	if m.NumGC > 0 {
		idx := (m.NumGC - 1) % 256
		o.ObserveInt64(rm.gcPauseLast, int64(m.PauseNs[idx]))
	}

	o.ObserveFloat64(rm.gcCPUFraction, m.GCCPUFraction)
	o.ObserveInt64(rm.nextGC, int64(m.NextGC))

	// Goroutines and threads
	o.ObserveInt64(rm.goroutines, int64(runtime.NumGoroutine()))

	// NumCgoCall is not in MemStats, using a different approach
	numThreads, _ := runtime.ThreadCreateProfile(nil)
	o.ObserveInt64(rm.threads, int64(numThreads))

	cgoCalls := runtime.NumCgoCall()
	if rm.lastCgoCalls > 0 {
		o.ObserveInt64(rm.cgoCalls, cgoCalls-rm.lastCgoCalls)
	}
	rm.lastCgoCalls = cgoCalls

	// CPU Usage
	var rusage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage); err == nil {
		user := float64(rusage.Utime.Sec) + float64(rusage.Utime.Usec)/1e6
		sys := float64(rusage.Stime.Sec) + float64(rusage.Stime.Usec)/1e6
		o.ObserveFloat64(rm.cpuUser, user)
		o.ObserveFloat64(rm.cpuSystem, sys)
	}
}

// GetMemoryStats returns the current Go runtime memory statistics.
//
// It wraps runtime.ReadMemStats and provides an instantaneous snapshot
// of memory allocation and garbage collection state. This helper is
// primarily intended for debugging or manual instrumentation.
func GetMemoryStats() runtime.MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m
}

// ForceGC triggers an explicit garbage collection cycle.
//
// Use with caution in production systems, as it can introduce latency
// and performance overhead. Intended for testing, benchmarking, or
// forcing GC prior to critical memory operations.
func ForceGC() {
	runtime.GC()
}

// MemoryUsageMB reports the current heap memory allocation in megabytes.
//
// This provides a simplified and human-readable metric for dashboards,
// logs, or lightweight monitoring systems.
func MemoryUsageMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / 1024 / 1024
}

// NumGoroutines returns the number of currently active goroutines.
//
// It wraps runtime.NumGoroutine() for convenience and can be used in
// lightweight monitoring scenarios.
func NumGoroutines() int {
	return runtime.NumGoroutine()
}

// RuntimeMonitor manages a background monitoring loop for runtime metrics.
//
// Although runtime metrics are collected asynchronously by OpenTelemetry,
// this structure can be used to trigger additional tasks at a fixed interval,
// such as structured logging or system health verification.
//
// The monitor can be safely started and stopped using its lifecycle methods.
type RuntimeMonitor struct {
	metrics  *RuntimeMetrics
	interval time.Duration
	done     chan struct{}
}

// NewRuntimeMonitor creates a new RuntimeMonitor that operates at the
// specified interval. If the interval is set to zero, a default of
// 10 seconds is applied.
//
// The monitor requires an initialized RuntimeMetrics instance to operate.
func NewRuntimeMonitor(metrics *RuntimeMetrics, interval time.Duration) *RuntimeMonitor {
	if interval == 0 {
		interval = 10 * time.Second
	}
	return &RuntimeMonitor{
		metrics:  metrics,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start launches the monitoring loop.
//
// The loop runs until either Stop() is called or the provided context is
// cancelled. While metrics are automatically collected via OpenTelemetry
// callbacks, this loop can be extended to perform additional diagnostics
// or custom periodic actions.
func (rm *RuntimeMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(rm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Metrics are already collected automatically through the callback.
			// This loop can be used for logging or additional operations.
		case <-rm.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stop gracefully terminates the monitoring loop started by Start.
//
// It ensures a clean shutdown of background operations.
func (rm *RuntimeMonitor) Stop() {
	close(rm.done)
}
