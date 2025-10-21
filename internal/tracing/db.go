package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// DBTracing provides OpenTelemetry distributed tracing for database operations.
//
// It creates spans for database queries with semantic conventions following
// OpenTelemetry standards for database client instrumentation.
// Spans include operation type, table name, duration, and error information.
type DBTracing struct {
	tracer trace.Tracer
}

// NewDBTracing initializes database tracing instrumentation.
//
// The tracer parameter should be obtained from a configured TracerProvider.
// The resulting DBTracing instance can be safely reused across multiple
// database clients and connection pools.
//
// Example:
//
//	tracer := otel.Tracer("myapp/db")
//	dbTracing := NewDBTracing(tracer)
//
// Production recommendations:
//   - Instantiate once per process or connection pool.
//   - Create spans for all database operations.
//   - Correlate with DB metrics for complete observability.
func NewDBTracing(tracer trace.Tracer) *DBTracing {
	return &DBTracing{
		tracer: tracer,
	}
}

// StartSpan creates a new span for a database operation.
//
// The span should be ended with span.End() when the operation completes.
// Use RecordError() if the operation fails.
//
// Example:
//
//	ctx, span := dbTracing.StartSpan(ctx, "SELECT", "users")
//	defer span.End()
//
//	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = ?", id)
//	if err != nil {
//	    dbTracing.RecordError(span, err)
//	    return err
//	}
//
// Production recommendations:
//   - Always call span.End() using defer.
//   - Record errors immediately when they occur.
//   - Use consistent operation and table names for better aggregation.
func (d *DBTracing) StartSpan(ctx context.Context, operation, table string) (context.Context, trace.Span) {
	spanName := "db." + operation

	ctx, span := d.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "sql"),
			attribute.String("db.operation", operation),
			attribute.String("db.collection.name", table),
		),
	)

	return ctx, span
}

// RecordQuery creates and records a complete database query span.
//
// This is a convenience method that creates a span, records the duration,
// and handles errors in a single call. Use this when you want automatic
// span management.
//
// Example:
//
//	start := time.Now()
//	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
//	dbTracing.RecordQuery(ctx, "SELECT", "users", time.Since(start), err)
//
// For more control over span lifecycle, use StartSpan() instead.
//
// Production recommendations:
//   - Use this for simple query recording.
//   - For complex operations with multiple steps, use StartSpan().
func (d *DBTracing) RecordQuery(
	ctx context.Context,
	operation string,
	table string,
	duration time.Duration,
	err error,
) {
	spanName := "db." + operation

	_, span := d.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "sql"),
			attribute.String("db.operation", operation),
			attribute.String("db.collection.name", table),
			attribute.Float64("db.duration", duration.Seconds()),
		),
	)
	defer span.End()

	if err != nil {
		d.RecordError(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// RecordError records an error in the current span.
//
// This sets the span status to Error and records the error message
// as a span event. Call this whenever a database operation fails.
//
// Example:
//
//	ctx, span := dbTracing.StartSpan(ctx, "INSERT", "users")
//	defer span.End()
//
//	_, err := db.ExecContext(ctx, query, args...)
//	if err != nil {
//	    dbTracing.RecordError(span, err)
//	    return err
//	}
//
// Production recommendations:
//   - Always record errors for failed operations.
//   - Combine with metrics error counters for alerting.
func (d *DBTracing) RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.Bool("error", true))
	}
}

// SetRowsAffected records the number of rows affected by a database operation.
//
// This is useful for INSERT, UPDATE, DELETE operations to track
// the impact of the query.
//
// Example:
//
//	result, err := db.ExecContext(ctx, "DELETE FROM users WHERE inactive = true")
//	if err == nil {
//	    rowsAffected, _ := result.RowsAffected()
//	    dbTracing.SetRowsAffected(span, rowsAffected)
//	}
func (d *DBTracing) SetRowsAffected(span trace.Span, rows int64) {
	span.SetAttributes(attribute.Int64("db.rows_affected", rows))
}
