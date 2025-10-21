package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// HTTPMetrics provides production-ready OpenTelemetry instruments
// for HTTP server observability.
//
// It tracks total request count, duration distributions, request and response sizes,
// and the number of active requests currently being processed. All metrics comply
// with the OpenTelemetry semantic conventions (`http.server.*`).
//
// This struct is designed for high-throughput environments and can be safely shared
// across multiple HTTP handlers or middleware components.
type HTTPMetrics struct {
	requestsTotal   metric.Int64Counter
	requestDuration metric.Float64Histogram
	requestSize     metric.Int64Histogram
	responseSize    metric.Int64Histogram
	activeRequests  metric.Int64UpDownCounter
}

// NewHTTPMetrics initializes and registers a complete set of HTTP server metrics.
//
// Each instrument is configured with explicit bucket boundaries suitable for
// web traffic latency and payload size analysis. The resulting HTTPMetrics instance
// can be reused safely across multiple servers or routes.
//
// Example:
//
//	meter := global.Meter("myapp/http")
//	httpMetrics, err := NewHTTPMetrics(meter)
//	if err != nil {
//	    log.Fatal("failed to initialize HTTP metrics", err)
//	}
//
// Production recommendations:
//   - Instantiate once per service process to ensure consistent aggregation.
//   - Combine with tracing spans for end-to-end latency correlation.
//   - Follow OpenTelemetry's semantic naming to align with dashboards.
func NewHTTPMetrics(meter metric.Meter) (*HTTPMetrics, error) {
	requestsTotal, err := meter.Int64Counter(
		"http.server.requests",
		metric.WithDescription("Total number of HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram(
		"http.server.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
	)
	if err != nil {
		return nil, err
	}

	requestSize, err := meter.Int64Histogram(
		"http.server.request.size",
		metric.WithDescription("HTTP request size in bytes"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(100, 1000, 10000, 100000, 1000000, 10000000),
	)
	if err != nil {
		return nil, err
	}

	responseSize, err := meter.Int64Histogram(
		"http.server.response.size",
		metric.WithDescription("HTTP response size in bytes"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(100, 1000, 10000, 100000, 1000000, 10000000),
	)
	if err != nil {
		return nil, err
	}

	activeRequests, err := meter.Int64UpDownCounter(
		"http.server.active_requests",
		metric.WithDescription("Number of active HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	return &HTTPMetrics{
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
		requestSize:     requestSize,
		responseSize:    responseSize,
		activeRequests:  activeRequests,
	}, nil
}

// RecordRequest records all relevant metrics for a completed HTTP request.
//
// It updates the total request count, duration histogram, and size distributions
// for both request and response payloads. Attributes include method, route,
// and status code for fine-grained observability.
//
// Example:
//
//	start := time.Now()
//	resp, err := handler(req)
//	httpMetrics.RecordRequest(
//	    ctx,
//	    req.Method,
//	    "/api/v1/users",
//	    resp.StatusCode,
//	    time.Since(start),
//	    req.ContentLength,
//	    resp.ContentLength,
//	)
//
// Production recommendations:
//   - Invoke this after each request completes.
//   - Ensure route labels are normalized (avoid user-specific paths).
//   - Correlate request duration with error rate to detect latency regressions.
func (h *HTTPMetrics) RecordRequest(
	ctx context.Context,
	method, route string,
	statusCode int,
	duration time.Duration,
	requestSize, responseSize int64,
) {
	attrs := metric.WithAttributes(
		attribute.String("http.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.status_code", statusCode),
	)

	h.requestsTotal.Add(ctx, 1, attrs)
	h.requestDuration.Record(ctx, duration.Seconds(), attrs)

	if requestSize > 0 {
		h.requestSize.Record(ctx, requestSize,
			metric.WithAttributes(
				attribute.String("http.method", method),
				attribute.String("http.route", route),
			))
	}

	h.responseSize.Record(ctx, responseSize, attrs)
}

// IncrementActiveRequests increments the counter tracking active HTTP requests.
//
// Call this when a new request starts processing.
//
// Example:
//
//	httpMetrics.IncrementActiveRequests(ctx)
//
// Production recommendations:
//   - Use middleware to increment/decrement automatically.
//   - Useful for identifying overload or concurrency spikes.
func (h *HTTPMetrics) IncrementActiveRequests(ctx context.Context, method string) {
	h.activeRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("http.method", method),
	))
}

// DecrementActiveRequests decrements the counter tracking active HTTP requests.
//
// Call this when a request completes or is aborted.
//
// Example:
//
//	defer httpMetrics.DecrementActiveRequests(ctx)
//
// Production recommendations:
//   - Always ensure balanced increments and decrements.
//   - Combine with circuit breakers to detect overload conditions.
func (h *HTTPMetrics) DecrementActiveRequests(ctx context.Context, method string) {
	h.activeRequests.Add(ctx, -1, metric.WithAttributes(
		attribute.String("http.method", method),
	))
}

// Middleware returns a Chi-compatible middleware that automatically
// records HTTP metrics for all requests.
//
// The middleware tracks:
//   - Total requests count
//   - Request duration
//   - Request and response sizes
//   - Active concurrent requests
//
// Example usage with Chi router:
//
//	httpMetrics, _ := metrics.NewHTTPMetrics(meter)
//	router := chi.NewRouter()
//	router.Use(httpMetrics.Middleware())
//	router.Get("/api/users", handler)
//
// The middleware extracts the route pattern from Chi's RouteContext,
// ensuring that metrics are grouped by route template (e.g., "/users/{id}")
// rather than individual paths (e.g., "/users/123").
//
// Production recommendations:
//   - Mount early in the middleware chain for accurate timing.
//   - Combine with error handling middleware to capture all requests.
//   - Use Chi's route patterns for consistent metric labels.
func (h *HTTPMetrics) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := r.Context()

			h.IncrementActiveRequests(ctx, r.Method)
			defer h.DecrementActiveRequests(ctx, r.Method)

			// Wrap response writer to capture status and bytes
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			// Process request
			next.ServeHTTP(ww, r)

			// Record metrics after request completes (DOPO - con route pattern)
			duration := time.Since(start)
			route := getRoutePattern(r)

			h.RecordRequest(
				ctx,
				r.Method,
				route,
				ww.Status(),
				duration,
				r.ContentLength,
				int64(ww.BytesWritten()),
			)
		})
	}
}

// getRoutePattern extracts the route pattern from Chi router context.
// Falls back to the request path if no route pattern is found.
func getRoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx != nil && rctx.RoutePattern() != "" {
		return rctx.RoutePattern()
	}
	return r.URL.Path
}
