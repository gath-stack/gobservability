package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DBMetrics provides strongly-typed OpenTelemetry instruments
// for monitoring database client performance and reliability.
//
// It tracks query durations, operation counts, connection usage,
// and error occurrences across any SQL or NoSQL backend.
// Metrics follow OpenTelemetry semantic conventions (`db.client.*`)
// and are suitable for production-grade observability pipelines.
type DBMetrics struct {
	queryDuration metric.Float64Histogram
	queryTotal    metric.Int64Counter
	connections   metric.Int64UpDownCounter
	errors        metric.Int64Counter
}

// NewDBMetrics initializes and registers all database-related metrics.
//
// Each metric is configured with explicit boundaries and semantic naming
// aligned with OpenTelemetry standards. The resulting DBMetrics instance
// can be safely reused across multiple database clients or connection pools.
//
// Example:
//
//	meter := global.Meter("myapp/db")
//	dbMetrics, err := NewDBMetrics(meter)
//	if err != nil {
//	    log.Fatal("failed to initialize DB metrics", err)
//	}
//
// Production recommendations:
//   - Instantiate this once per process or connection pool.
//   - Record all queries, including read-only and transactional operations.
//   - Correlate query latency with tracing spans for end-to-end insight.
func NewDBMetrics(meter metric.Meter) (*DBMetrics, error) {
	queryDuration, err := meter.Float64Histogram(
		"db.client.operation.duration",
		metric.WithDescription("Database operation duration"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5),
	)
	if err != nil {
		return nil, err
	}

	queryTotal, err := meter.Int64Counter(
		"db.client.operation.count",
		metric.WithDescription("Total number of database operations"),
		metric.WithUnit("{operation}"),
	)
	if err != nil {
		return nil, err
	}

	connections, err := meter.Int64UpDownCounter(
		"db.client.connections.usage",
		metric.WithDescription("Number of active database connections"),
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, err
	}

	errors, err := meter.Int64Counter(
		"db.client.errors",
		metric.WithDescription("Database errors"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	return &DBMetrics{
		queryDuration: queryDuration,
		queryTotal:    queryTotal,
		connections:   connections,
		errors:        errors,
	}, nil
}

// RecordQuery records execution metrics for a single database query.
//
// It captures latency (in seconds), total query count, and error occurrences.
// The method automatically attaches contextual attributes such as operation type
// and table (collection) name.
//
// Example:
//
//	start := time.Now()
//	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
//	dbMetrics.RecordQuery(ctx, "SELECT", "users", time.Since(start), err == nil)
//
// Production recommendations:
//   - Call this after every database interaction.
//   - Maintain consistent `db.operation` and `db.collection.name` attributes.
//   - Use `success=false` for retries or failed queries to improve accuracy.
func (d *DBMetrics) RecordQuery(
	ctx context.Context,
	operation string, // SELECT, INSERT, UPDATE, DELETE
	table string,
	duration time.Duration,
	success bool,
) {
	attrs := metric.WithAttributes(
		attribute.String("db.operation", operation),
		attribute.String("db.collection.name", table),
		attribute.Bool("error", !success),
	)

	d.queryDuration.Record(ctx, duration.Seconds(), attrs)
	d.queryTotal.Add(ctx, 1, attrs)

	if !success {
		d.errors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("db.operation", operation),
			attribute.String("db.collection.name", table),
		))
	}
}

// UpdateConnections updates the number of active database connections.
//
// This metric should be used to reflect connection pool activity.
// Increment by positive values when a connection is opened,
// and decrement when closed.
//
// Example:
//
//	dbMetrics.UpdateConnections(ctx, +1) // on connection open
//	dbMetrics.UpdateConnections(ctx, -1) // on connection close
//
// Production recommendations:
//   - Use this in connection pool hooks or middleware.
//   - Monitor for connection leaks or saturation in long-lived services.
func (d *DBMetrics) UpdateConnections(ctx context.Context, count int64) {
	d.connections.Add(ctx, count)
}
