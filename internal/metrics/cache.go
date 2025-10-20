package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CacheMetrics provides strongly-typed OpenTelemetry instruments
// for monitoring cache performance and behavior across Redis, Memcached,
// or in-memory cache implementations.
//
// It tracks operation counts, hit/miss ratios, latency distributions,
// item sizes, connection changes, eviction rates, and error occurrences.
// All metrics are pre-registered with semantic naming and recommended units
// for production-grade observability.
type CacheMetrics struct {
	operations  metric.Int64Counter
	hits        metric.Int64Counter
	misses      metric.Int64Counter
	latency     metric.Float64Histogram
	itemSize    metric.Int64Histogram
	evictions   metric.Int64Counter
	connections metric.Int64UpDownCounter
	errors      metric.Int64Counter
}

// NewCacheMetrics initializes and registers all cache-related metric instruments.
//
// Each metric follows OpenTelemetry naming conventions and includes explicit
// bucket boundaries for latency and item size distributions.
//
// The returned CacheMetrics instance can be safely reused across multiple cache
// clients or services, as all instruments are thread-safe.
//
// Example:
//
//	meter := global.Meter("myapp/cache")
//	cacheMetrics, err := NewCacheMetrics(meter)
//	if err != nil {
//	    log.Fatal("failed to initialize cache metrics", err)
//	}
//
// Production recommendations:
//   - Instantiate this once per process to avoid redundant registration.
//   - Combine with tracing spans for full cache performance visibility.
//   - Use consistent `cache.operation` attributes for better aggregation.
func NewCacheMetrics(meter metric.Meter) (*CacheMetrics, error) {
	operations, err := meter.Int64Counter(
		"cache.operations",
		metric.WithDescription("Total cache operations"),
		metric.WithUnit("{operation}"),
	)
	if err != nil {
		return nil, err
	}

	hits, err := meter.Int64Counter(
		"cache.hits",
		metric.WithDescription("Cache hits"),
		metric.WithUnit("{hit}"),
	)
	if err != nil {
		return nil, err
	}

	misses, err := meter.Int64Counter(
		"cache.misses",
		metric.WithDescription("Cache misses"),
		metric.WithUnit("{miss}"),
	)
	if err != nil {
		return nil, err
	}

	latency, err := meter.Float64Histogram(
		"cache.operation.duration",
		metric.WithDescription("Cache operation duration"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1),
	)
	if err != nil {
		return nil, err
	}

	itemSize, err := meter.Int64Histogram(
		"cache.item.size",
		metric.WithDescription("Size of cached items"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(100, 1000, 10000, 100000, 1000000),
	)
	if err != nil {
		return nil, err
	}

	evictions, err := meter.Int64Counter(
		"cache.evictions",
		metric.WithDescription("Cache evictions"),
		metric.WithUnit("{eviction}"),
	)
	if err != nil {
		return nil, err
	}

	connections, err := meter.Int64UpDownCounter(
		"cache.connections",
		metric.WithDescription("Active cache connections"),
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, err
	}

	errors, err := meter.Int64Counter(
		"cache.errors",
		metric.WithDescription("Cache errors"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	return &CacheMetrics{
		operations:  operations,
		hits:        hits,
		misses:      misses,
		latency:     latency,
		itemSize:    itemSize,
		evictions:   evictions,
		connections: connections,
		errors:      errors,
	}, nil
}

// RecordGet records a cache GET operation with its latency, hit status,
// and item size when available.
//
// It increments both the total operation count and the corresponding hit/miss
// counters. The operation duration is recorded in seconds.
//
// Example:
//
//	start := time.Now()
//	value, ok := cache.Get(key)
//	cacheMetrics.RecordGet(ctx, "get", ok, time.Since(start), int64(len(value)))
//
// Production recommendations:
//   - Always measure latency for GET operations.
//   - Include consistent `cache.operation` attributes across services.
func (c *CacheMetrics) RecordGet(ctx context.Context, operation string, hit bool, duration time.Duration, size int64) {
	attrs := metric.WithAttributes(
		attribute.String("cache.operation", operation),
		attribute.Bool("cache.hit", hit),
	)

	c.operations.Add(ctx, 1, attrs)
	c.latency.Record(ctx, duration.Seconds(), attrs)

	if hit {
		c.hits.Add(ctx, 1)
		if size > 0 {
			c.itemSize.Record(ctx, size)
		}
	} else {
		c.misses.Add(ctx, 1)
	}
}

// RecordSet records a cache SET operation, including latency, item size,
// and success state.
//
// When a SET operation fails, the method increments the `cache.errors` counter.
// Size distributions and latency histograms are updated automatically.
//
// Example:
//
//	start := time.Now()
//	ok := cache.Set(key, value)
//	cacheMetrics.RecordSet(ctx, time.Since(start), int64(len(value)), ok)
//
// Production recommendations:
//   - Record both duration and success/failure status.
//   - For high-throughput caches, ensure histogram aggregation is optimized.
func (c *CacheMetrics) RecordSet(ctx context.Context, duration time.Duration, size int64, success bool) {
	attrs := metric.WithAttributes(
		attribute.String("cache.operation", "set"),
	)

	c.operations.Add(ctx, 1, attrs)
	c.latency.Record(ctx, duration.Seconds(), attrs)

	if size > 0 {
		c.itemSize.Record(ctx, size)
	}

	if !success {
		c.errors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("cache.operation", "set"),
		))
	}
}

// RecordDelete records a cache DELETE operation with its latency and success flag.
//
// Errors during deletion increment the `cache.errors` counter.
// Duration is captured in seconds.
//
// Example:
//
//	start := time.Now()
//	ok := cache.Delete(key)
//	cacheMetrics.RecordDelete(ctx, time.Since(start), ok)
//
// Production recommendations:
//   - Use this in cleanup jobs and TTL-based eviction logic.
//   - Include `cache.operation="delete"` for unified metric labeling.
func (c *CacheMetrics) RecordDelete(ctx context.Context, duration time.Duration, success bool) {
	attrs := metric.WithAttributes(
		attribute.String("cache.operation", "delete"),
	)

	c.operations.Add(ctx, 1, attrs)
	c.latency.Record(ctx, duration.Seconds(), attrs)

	if !success {
		c.errors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("cache.operation", "delete"),
		))
	}
}

// RecordEviction increments the total eviction counter.
//
// This should be called whenever an item is evicted by cache policy,
// e.g., LRU or TTL expiration.
//
// Example:
//
//	cacheMetrics.RecordEviction(ctx, 1)
//
// Production recommendations:
//   - Correlate with hit/miss ratio for cache efficiency analysis.
func (c *CacheMetrics) RecordEviction(ctx context.Context, count int64) {
	c.evictions.Add(ctx, count)
}

// UpdateConnections adjusts the number of active cache connections.
//
// Use a positive value to increment, or a negative value to decrement
// the current active connection count.
//
// Example:
//
//	cacheMetrics.UpdateConnections(ctx, +1) // on connect
//	cacheMetrics.UpdateConnections(ctx, -1) // on disconnect
//
// Production recommendations:
//   - Track connection churn to identify connection leaks or pool exhaustion.
//   - Combine with latency histograms for end-to-end performance insights.
func (c *CacheMetrics) UpdateConnections(ctx context.Context, count int64) {
	c.connections.Add(ctx, count)
}
