# gobservability

Production-ready observability stack for Go applications with OpenTelemetry integration.

## Overview

`gobservability` provides a unified, zero-configuration observability solution for Go services. It handles metrics, traces, logs, and profiling through a single initialization call, with strict environment-based configuration for production safety.

## Features

- **Zero-configuration setup** - Single `Init()` call to initialize everything
- **Environment-based configuration** - Strict validation with fail-fast behavior
- **OpenTelemetry integration** - OTLP export for metrics, traces, and logs
- **Pre-configured metrics** - HTTP, database, cache, runtime, and system metrics ready to use
- **Graceful shutdown** - Proper cleanup and metric flushing
- **Production-ready** - Battle-tested defaults and comprehensive error handling

## Installation

```bash
go get github.com/gath-stack/gobservability
```

## Quick Start

```go
package main

import (
    "context"
    "net/http"
    
    "github.com/go-chi/chi/v5"
    observability "github.com/gath-stack/gobservability"
    "go.uber.org/zap"
)

func main() {
    log, _ := zap.NewProduction()
    
    // Initialize observability stack
    stack, err := observability.Init(log, nil)
    if err != nil {
        log.Fatal("failed to initialize observability", zap.Error(err))
    }
    defer func() {
        if err := stack.Shutdown(context.Background()); err != nil {
            log.Error("failed to shutdown observability", zap.Error(err))
        }
    }()
    
    // Setup HTTP server with automatic metrics
    router := chi.NewRouter()
    router.Use(stack.HTTPMetricsMiddleware())
    router.Get("/api/users", handleGetUsers)
    
    http.ListenAndServe(":8080", router)
}

func handleGetUsers(w http.ResponseWriter, r *http.Request) {
    // Your handler code here
}
```

## Configuration

All configuration is done through environment variables. The package enforces strict validation and fails fast on misconfiguration.

### Required Variables

```bash
APP_NAME=my-service
APP_VERSION=1.0.0
APP_ENV=production  # development, staging, or production
```

### Feature Flags

Enable specific observability components:

```bash
OBSERVABILITY_METRICS_ENABLED=true
OBSERVABILITY_TRACING_ENABLED=false
OBSERVABILITY_LOGS_ENABLED=false
OBSERVABILITY_PROFILING_ENABLED=false
```

### Endpoints

Required when features are enabled:

```bash
OBSERVABILITY_OTLP_ENDPOINT=otel-collector:4317
OBSERVABILITY_PYROSCOPE_SERVER_ADDRESS=http://pyroscope:4040  # only for profiling
```

### Optional Configuration

```bash
OBSERVABILITY_METRIC_EXPORT_INTERVAL=10      # seconds (default: 10)
OBSERVABILITY_TRACE_SAMPLING_RATE=0.1        # 0.0-1.0 (default: environment-based)
OBSERVABILITY_METRIC_BATCH_SIZE=1024         # default: 1024
OBSERVABILITY_TRACE_BATCH_SIZE=512           # default: 512
DEPLOYMENT_ID=deployment-xyz                 # optional deployment identifier
```

## Usage Examples

### HTTP Metrics

Automatic HTTP request tracking with Chi router:

```go
stack, _ := observability.Init(log, nil)
router := chi.NewRouter()
router.Use(stack.HTTPMetricsMiddleware())
```

Manual HTTP metrics recording:

```go
start := time.Now()
// ... handle request ...
duration := time.Since(start)

stack.HTTP.RecordRequest(ctx, 
    "GET", 
    "/api/users", 
    200, 
    duration, 
    requestSize, 
    responseSize)
```

### Database Metrics

```go
start := time.Now()
rows, err := db.Query(ctx, "SELECT * FROM users WHERE active = ?", true)
duration := time.Since(start)

stack.DB.RecordQuery(ctx, "SELECT", "users", duration, err == nil)
```

Track connection pool:

```go
// Connection acquired
stack.DB.UpdateConnections(ctx, +1)
defer stack.DB.UpdateConnections(ctx, -1)
```

### Cache Metrics

```go
start := time.Now()
value, found := cache.Get(key)
duration := time.Since(start)

var size int64
if found {
    size = int64(len(value))
}

stack.Cache.RecordGet(ctx, "get", found, duration, size)
```

### Runtime and System Metrics

Runtime and system metrics are collected automatically. No manual instrumentation required.

Runtime metrics include:
- Goroutine count
- Memory usage (heap, stack, GC stats)
- GC pause times
- CPU usage

System metrics include:
- CPU utilization
- Disk usage and I/O
- Network I/O

To disable system metrics:

```go
stack, err := observability.Init(log, &observability.InitOptions{
    DisableSystemMetrics: true,
})
```

### Custom Metrics

For advanced use cases, access the OpenTelemetry meter directly:

```go
meter := stack.Meter()

counter, _ := meter.Int64Counter(
    "custom.operations.total",
    metric.WithDescription("Total custom operations"),
)

counter.Add(ctx, 1, metric.WithAttributes(
    attribute.String("operation", "process"),
    attribute.String("status", "success"),
))
```

## Architecture

### Components

- **Config**: Environment-based configuration with validation
- **Metrics**: OpenTelemetry metrics with OTLP export
- **HTTP Metrics**: Request count, duration, size, status codes
- **DB Metrics**: Query performance, connection pool, operation types
- **Cache Metrics**: Hit/miss rates, operation duration, data size
- **Runtime Metrics**: Go runtime statistics (automatic)
- **System Metrics**: OS-level resource usage (automatic)

### Metric Export

All metrics are exported via OTLP (OpenTelemetry Protocol) to a configured collector endpoint. The default export interval is 10 seconds.

## Best Practices

### Initialization

Always initialize observability early in your application startup:

```go
func main() {
    log := initLogger()
    
    stack, err := observability.Init(log, nil)
    if err != nil {
        log.Fatal("observability init failed", zap.Error(err))
    }
    defer func() {
        if err := stack.Shutdown(context.Background()); err != nil {
            log.Error("observability shutdown failed", zap.Error(err))
        }
    }()
    
    // Continue with application setup
}
```

### Graceful Shutdown

Always call `Shutdown()` to flush metrics and clean up resources:

```go
defer func() {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    if err := stack.Shutdown(ctx); err != nil {
        log.Error("failed to shutdown observability", zap.Error(err))
    }
}()
```

### Environment-Specific Configuration

Use different sampling rates and export intervals per environment:

Development:
```bash
APP_ENV=development
OBSERVABILITY_TRACE_SAMPLING_RATE=1.0  # 100% sampling
OBSERVABILITY_METRIC_EXPORT_INTERVAL=5  # faster feedback
```

Production:
```bash
APP_ENV=production
OBSERVABILITY_TRACE_SAMPLING_RATE=0.1  # 10% sampling
OBSERVABILITY_METRIC_EXPORT_INTERVAL=10
```

### Error Handling

The package follows fail-fast principles. Configuration errors cause immediate failure:

```go
// This will fail fast if environment is misconfigured
stack, err := observability.Init(log, nil)
if err != nil {
    // Log the error and exit - don't continue with invalid config
    log.Fatal("observability configuration invalid", zap.Error(err))
}
```

### Context Propagation

Always pass context through your call chain for proper metric attribution:

```go
func handleRequest(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    
    users, err := getUsersFromDB(ctx)  // Pass context down
    if err != nil {
        // Handle error
    }
}

func getUsersFromDB(ctx context.Context) ([]User, error) {
    start := time.Now()
    // ... database query ...
    
    stack.DB.RecordQuery(ctx, "SELECT", "users", time.Since(start), true)
    return users, nil
}
```

## Integration Examples

### Docker Compose

```yaml
version: '3.8'

services:
  app:
    build: .
    environment:
      - APP_NAME=my-service
      - APP_VERSION=1.0.0
      - APP_ENV=production
      - OBSERVABILITY_METRICS_ENABLED=true
      - OBSERVABILITY_OTLP_ENDPOINT=otel-collector:4317
    depends_on:
      - otel-collector

  otel-collector:
    image: otel/opentelemetry-collector:latest
    ports:
      - "4317:4317"
    volumes:
      - ./otel-config.yaml:/etc/otel-config.yaml
    command: ["--config=/etc/otel-config.yaml"]
```

### Kubernetes

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
data:
  APP_NAME: "my-service"
  APP_VERSION: "1.0.0"
  APP_ENV: "production"
  OBSERVABILITY_METRICS_ENABLED: "true"
  OBSERVABILITY_OTLP_ENDPOINT: "otel-collector.observability.svc.cluster.local:4317"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-service
spec:
  template:
    spec:
      containers:
      - name: app
        image: my-service:1.0.0
        envFrom:
        - configMapRef:
            name: app-config
```

## Troubleshooting

### Metrics not appearing

Check that:
1. `OBSERVABILITY_METRICS_ENABLED=true` is set
2. `OBSERVABILITY_OTLP_ENDPOINT` points to a valid collector
3. The OTLP collector is reachable from your service
4. The collector is properly configured to receive OTLP metrics

### High memory usage

Adjust batch sizes to reduce memory footprint:

```bash
OBSERVABILITY_METRIC_BATCH_SIZE=512  # Reduce from default 1024
OBSERVABILITY_TRACE_BATCH_SIZE=256   # Reduce from default 512
```

### Initialization failures

The package validates configuration strictly. Common issues:

- Missing required environment variables (`APP_NAME`, `APP_VERSION`, `APP_ENV`)
- Invalid `APP_ENV` value (must be: development, staging, or production)
- Invalid `OBSERVABILITY_OTLP_ENDPOINT` format (must be `host:port`)
- OTLP endpoint required when features are enabled

Check logs for detailed validation errors.

## Requirements

- Go 1.21 or later
- OpenTelemetry Collector or compatible OTLP endpoint

## Dependencies

- `go.opentelemetry.io/otel`
- `go.opentelemetry.io/otel/sdk`
- `go.opentelemetry.io/otel/exporters/otlp/otlpmetric`
- `go.uber.org/zap` (for logging interface)

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome. Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes with tests
4. Submit a pull request

Ensure all tests pass and code follows the existing style.

## Support

For issues and questions:
- GitHub Issues: https://github.com/gath-stack/gobservability/issues
- Documentation: https://pkg.go.dev/github.com/gath-stack/gobservability