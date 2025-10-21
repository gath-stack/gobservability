package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// CacheTracing provides OpenTelemetry distributed tracing for cache operations.
//
// It creates spans for cache operations (GET, SET, DELETE) with semantic
// conventions following OpenTelemetry standards for cache instrumentation.
// Spans include operation type, cache hit/miss status, and error information.
type CacheTracing struct {
	tracer trace.Tracer
}

// NewCacheTracing initializes cache tracing instrumentation.
//
// The tracer parameter should be obtained from a configured TracerProvider.
// The resulting CacheTracing instance can be safely reused across multiple
// cache clients (Redis, Memcached, etc.).
//
// Example:
//
//	tracer := otel.Tracer("myapp/cache")
//	cacheTracing := NewCacheTracing(tracer)
//
// Production recommendations:
//   - Instantiate once per application.
//   - Trace all cache operations for complete visibility.
//   - Correlate with cache metrics for performance analysis.
func NewCacheTracing(tracer trace.Tracer) *CacheTracing {
	return &CacheTracing{
		tracer: tracer,
	}
}

// StartSpan creates a new span for a cache operation.
//
// The span should be ended with span.End() when the operation completes.
// Use RecordHit() or RecordError() to add operation-specific attributes.
//
// Example:
//
//	ctx, span := cacheTracing.StartSpan(ctx, "GET", "user:123")
//	defer span.End()
//
//	value, err := redis.Get(ctx, "user:123")
//	if err == redis.Nil {
//	    cacheTracing.RecordHit(span, false) // miss
//	} else if err != nil {
//	    cacheTracing.RecordError(span, err)
//	} else {
//	    cacheTracing.RecordHit(span, true) // hit
//	}
//
// Production recommendations:
//   - Always call span.End() using defer.
//   - Record hit/miss status for GET operations.
//   - Use consistent operation and key patterns for aggregation.
func (c *CacheTracing) StartSpan(ctx context.Context, operation, key string) (context.Context, trace.Span) {
	spanName := "cache." + operation

	ctx, span := c.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("cache.operation", operation),
			attribute.String("cache.key", key),
		),
	)

	return ctx, span
}

// RecordGet creates and records a complete cache GET operation span.
//
// This is a convenience method that creates a span, records the duration,
// hit/miss status, and handles errors in a single call.
//
// Example:
//
//	start := time.Now()
//	value, err := redis.Get(ctx, "user:123")
//	hit := err == nil
//	cacheTracing.RecordGet(ctx, "user:123", hit, time.Since(start), err)
//
// For more control over span lifecycle, use StartSpan() instead.
//
// Production recommendations:
//   - Use this for simple GET operations.
//   - For complex multi-step operations, use StartSpan().
func (c *CacheTracing) RecordGet(
	ctx context.Context,
	key string,
	hit bool,
	duration time.Duration,
	err error,
) {
	spanName := "cache.GET"

	_, span := c.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("cache.operation", "GET"),
			attribute.String("cache.key", key),
			attribute.Bool("cache.hit", hit),
			attribute.Float64("cache.duration", duration.Seconds()),
		),
	)
	defer span.End()

	if err != nil {
		c.RecordError(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// RecordSet creates and records a complete cache SET operation span.
//
// This is a convenience method that creates a span, records the duration,
// and handles errors in a single call.
//
// Example:
//
//	start := time.Now()
//	err := redis.Set(ctx, "user:123", userData, ttl)
//	cacheTracing.RecordSet(ctx, "user:123", time.Since(start), err)
//
// For more control over span lifecycle, use StartSpan() instead.
//
// Production recommendations:
//   - Use this for simple SET operations.
//   - Record TTL information if relevant to debugging.
func (c *CacheTracing) RecordSet(
	ctx context.Context,
	key string,
	duration time.Duration,
	err error,
) {
	spanName := "cache.SET"

	_, span := c.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("cache.operation", "SET"),
			attribute.String("cache.key", key),
			attribute.Float64("cache.duration", duration.Seconds()),
		),
	)
	defer span.End()

	if err != nil {
		c.RecordError(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// RecordDelete creates and records a complete cache DELETE operation span.
//
// This is a convenience method that creates a span, records the duration,
// and handles errors in a single call.
//
// Example:
//
//	start := time.Now()
//	err := redis.Del(ctx, "user:123")
//	cacheTracing.RecordDelete(ctx, "user:123", time.Since(start), err)
//
// For more control over span lifecycle, use StartSpan() instead.
func (c *CacheTracing) RecordDelete(
	ctx context.Context,
	key string,
	duration time.Duration,
	err error,
) {
	spanName := "cache.DELETE"

	_, span := c.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("cache.operation", "DELETE"),
			attribute.String("cache.key", key),
			attribute.Float64("cache.duration", duration.Seconds()),
		),
	)
	defer span.End()

	if err != nil {
		c.RecordError(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// RecordHit records whether a cache operation resulted in a hit or miss.
//
// This is typically used with GET operations after calling StartSpan().
//
// Example:
//
//	ctx, span := cacheTracing.StartSpan(ctx, "GET", "user:123")
//	defer span.End()
//
//	value, err := redis.Get(ctx, "user:123")
//	if err == redis.Nil {
//	    cacheTracing.RecordHit(span, false)
//	} else if err == nil {
//	    cacheTracing.RecordHit(span, true)
//	}
func (c *CacheTracing) RecordHit(span trace.Span, hit bool) {
	span.SetAttributes(attribute.Bool("cache.hit", hit))
}

// RecordError records an error in the current span.
//
// This sets the span status to Error and records the error message
// as a span event. Call this whenever a cache operation fails.
//
// Example:
//
//	ctx, span := cacheTracing.StartSpan(ctx, "SET", "user:123")
//	defer span.End()
//
//	err := redis.Set(ctx, key, value, ttl)
//	if err != nil {
//	    cacheTracing.RecordError(span, err)
//	    return err
//	}
//
// Production recommendations:
//   - Always record errors for failed operations.
//   - Combine with metrics error counters for alerting.
func (c *CacheTracing) RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.Bool("error", true))
	}
}

// SetItemSize records the size of a cache item.
//
// This is useful for tracking memory usage and identifying large items
// that may impact cache performance.
//
// Example:
//
//	ctx, span := cacheTracing.StartSpan(ctx, "GET", "user:123")
//	defer span.End()
//
//	value, err := redis.Get(ctx, "user:123")
//	if err == nil {
//	    cacheTracing.SetItemSize(span, int64(len(value)))
//	}
func (c *CacheTracing) SetItemSize(span trace.Span, size int64) {
	span.SetAttributes(attribute.Int64("cache.item.size", size))
}
