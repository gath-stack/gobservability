package tracing

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// HTTPTracing provides OpenTelemetry distributed tracing for HTTP requests.
//
// It creates spans for each HTTP request with semantic conventions following
// OpenTelemetry standards for HTTP server instrumentation.
// Spans include request method, URL, status code, and error information.
type HTTPTracing struct {
	tracer trace.Tracer
}

// NewHTTPTracing initializes HTTP tracing instrumentation.
//
// The tracer parameter should be obtained from a configured TracerProvider.
// The resulting HTTPTracing instance can be safely reused across multiple
// HTTP handlers and routers.
//
// Example:
//
//	tracer := otel.Tracer("myapp/http")
//	httpTracing := NewHTTPTracing(tracer)
//	router.Use(httpTracing.Middleware())
//
// Production recommendations:
//   - Instantiate once per application.
//   - Combine with HTTP metrics for complete observability.
//   - Use consistent span naming across services.
func NewHTTPTracing(tracer trace.Tracer) *HTTPTracing {
	return &HTTPTracing{
		tracer: tracer,
	}
}

// Middleware returns a Chi-compatible middleware that creates a span for each HTTP request.
//
// This middleware uses LAZY ROUTE PATTERN CAPTURE to avoid cardinality explosion.
// It creates a temporary span at the start, executes the handler (allowing Chi to
// populate RouteContext), then updates the span with the correct route pattern.
//
// The middleware:
//   - Extracts trace context from incoming request headers
//   - Creates a new span with temporary name
//   - Injects the span into the request context
//   - Executes the handler (Chi populates RouteContext during this)
//   - Updates span with correct route pattern
//   - Records request attributes (method, route pattern, status)
//   - Records errors if the status code indicates failure
//   - Properly ends the span after the request completes
//
// Example:
//
//	router := chi.NewRouter()
//	router.Use(httpTracing.Middleware())  // Global middleware
//	router.Get("/users/{id}", handler)    // Will be traced as "GET /users/{id}"
//
// The span is automatically propagated through the context and can be accessed
// by downstream operations (DB queries, cache operations, etc.).
//
// Production recommendations:
//   - Place this middleware early in the chain for complete coverage.
//   - Combine with metrics middleware for unified observability.
//   - Use trace sampling to control data volume in production.
func (h *HTTPTracing) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Start span with temporary name
			// We'll update it after the handler executes
			ctx, span := h.tracer.Start(ctx, "HTTP "+r.Method,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.scheme", scheme(r)),
					attribute.String("http.host", r.Host),
					attribute.String("http.user_agent", r.UserAgent()),
					attribute.String("http.client_ip", clientIP(r)),
				),
			)
			defer span.End()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Execute handler - Chi populates RouteContext during this
			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// NOW we can get the route pattern (lazy capture)
			routePattern := getRoutePattern(r)

			// Update span with the correct route pattern
			span.SetName(r.Method + " " + routePattern)

			// Record response attributes
			span.SetAttributes(
				attribute.String("http.route", routePattern),
				attribute.Int("http.status_code", wrapped.statusCode),
				attribute.Int64("http.response_content_length", wrapped.bytesWritten),
			)

			// Mark span as error if status >= 400
			if wrapped.statusCode >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.statusCode))
				span.SetAttributes(
					attribute.Bool("error", true),
				)
			} else {
				span.SetStatus(codes.Ok, "")
			}
		})
	}
}

// RecordRequest is a manual method to record HTTP request traces.
//
// This is provided for compatibility with custom HTTP clients or situations
// where the middleware cannot be used. For most use cases, the Middleware()
// approach is recommended.
//
// Example:
//
//	ctx, span := httpTracing.RecordRequest(ctx, "GET", "/api/users/{id}", 200, duration)
//	defer span.End()
//
// Production recommendations:
//   - Prefer the middleware for automatic instrumentation.
//   - Use this for HTTP client calls or non-standard servers.
func (h *HTTPTracing) RecordRequest(
	ctx context.Context,
	method string,
	path string,
	statusCode int,
	duration time.Duration,
) (context.Context, trace.Span) {
	spanName := method + " " + path

	ctx, span := h.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.route", path),
			attribute.Int("http.status_code", statusCode),
			attribute.Float64("http.duration", duration.Seconds()),
		),
	)

	if statusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(statusCode))
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return ctx, span
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}

// getRoutePattern extracts the route pattern from Chi's RouteContext.
// Returns the templated route (e.g., "/api/users/{id}") instead of the actual path.
// Falls back to the actual path if route pattern is not available.
func getRoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	// Fallback to actual path (for routes without patterns)
	return r.URL.Path
}

// scheme extracts the request scheme (http or https).
func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	return "http"
}

// clientIP extracts the real client IP from common headers.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}
