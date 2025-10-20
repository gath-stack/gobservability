package metrics

import (
	"go.opentelemetry.io/otel/metric"
)

// Counter creates a generic Int64 counter metric.
//
// A counter represents a monotonically increasing value, typically used
// for tracking events, requests, or other counts that only go up.
//
// Parameters:
// - meter: the OpenTelemetry Meter used to register the counter.
// - name: the metric name (should be globally unique).
// - description: human-readable description of the metric.
// - unit: the unit of measurement (e.g., "{request}", "By").
//
// Returns an Int64Counter ready to be incremented via Add().
func Counter(meter metric.Meter, name, description, unit string) (metric.Int64Counter, error) {
	return meter.Int64Counter(
		name,
		metric.WithDescription(description),
		metric.WithUnit(unit),
	)
}

// Histogram creates a generic Float64 histogram metric.
//
// A histogram records the distribution of values over time, allowing
// observation of percentiles, min/max, and other statistical summaries.
//
// Parameters:
// - meter: the OpenTelemetry Meter used to register the histogram.
// - name: the metric name (should be globally unique).
// - description: human-readable description of the metric.
// - unit: the unit of measurement (e.g., "ms", "By").
// - buckets: optional explicit bucket boundaries for the histogram.
//
// Returns a Float64Histogram ready to observe values.
func Histogram(meter metric.Meter, name, description, unit string, buckets []float64) (metric.Float64Histogram, error) {
	opts := []metric.Float64HistogramOption{
		metric.WithDescription(description),
		metric.WithUnit(unit),
	}

	if len(buckets) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(buckets...))
	}

	return meter.Float64Histogram(name, opts...)
}

// Gauge creates a generic Int64 up-down counter metric (used as a gauge).
//
// Gauges represent values that can go up or down, such as current memory usage,
// queue length, or number of active sessions.
//
// Parameters:
// - meter: the OpenTelemetry Meter used to register the gauge.
// - name: the metric name (should be globally unique).
// - description: human-readable description of the metric.
// - unit: the unit of measurement (e.g., "{session}", "By").
//
// Returns an Int64UpDownCounter that can be incremented or decremented.
func Gauge(meter metric.Meter, name, description, unit string) (metric.Int64UpDownCounter, error) {
	return meter.Int64UpDownCounter(
		name,
		metric.WithDescription(description),
		metric.WithUnit(unit),
	)
}
