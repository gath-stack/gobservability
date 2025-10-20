// Package observability provides a production-ready observability stack for Go applications.
//
// This package offers unified initialization and management of metrics, traces, logs, and profiles
// using OpenTelemetry standards. Configuration is strictly environment-based for production safety.
//
// # Features
//
//   - Zero-configuration setup with Init()
//   - Environment-only configuration with strict validation
//   - OpenTelemetry metrics export via OTLP
//   - Automatic runtime and system metrics collection
//   - Graceful shutdown with proper cleanup
//   - Fail-fast behavior for misconfiguration
//
// # Quick Start
//
//	import "github.com/gath-stack/gobservability"
//
//	func main() {
//	    log := logger.Get()
//
//	    // One line to initialize everything!
//	    stack, err := observability.Init(log, nil)
//	    if err != nil {
//	        log.Fatal("failed to init observability", zap.Error(err))
//	    }
//	    defer func() {
//			if err := observability.Shutdown(context.Background()); err != nil {
//				log.Error("Failed to shutdown observability", zap.Error(err))
//			}
//		}()
//
//	    // All metrics are ready to use
//	    stack.HTTP.RecordRequest(ctx, "GET", "/api/users", 200, duration, reqSize, respSize)
//	    stack.DB.RecordQuery(ctx, "SELECT", "users", duration, true)
//	    // Runtime and System metrics: automatic!
//	}
//
// # Environment Variables
//
// Required environment variables:
//   - APP_NAME: Service name
//   - APP_VERSION: Service version
//   - APP_ENV: Environment (development, staging, production)
//   - OBSERVABILITY_OTLP_ENDPOINT: OTLP collector endpoint (if features enabled)
package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gath-stack/gobservability/internal/config"
	metrics "github.com/gath-stack/gobservability/internal/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.uber.org/zap"
)

// Logger is the interface for logging operations used throughout the observability stack.
// It is compatible with zap.Logger and other structured logging packages that follow
// the same field-based logging pattern.
type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
}

// Stack represents a fully initialized observability stack with pre-configured metrics collectors.
//
// Stack provides convenient access to HTTP, database, cache, runtime, and system metrics
// without requiring manual initialization of each component. All metrics are ready to use
// immediately after calling Init().
//
// The Stack must be properly shut down using Shutdown() to ensure metrics are flushed
// and resources are cleaned up.
type Stack struct {
	obs *Observability
	log Logger

	// HTTP provides metrics for HTTP request/response tracking.
	HTTP *metrics.HTTPMetrics

	// DB provides metrics for database query operations.
	DB *metrics.DBMetrics

	// Cache provides metrics for cache operations (hits, misses, etc.).
	Cache *metrics.CacheMetrics

	// Runtime provides automatic Go runtime metrics (goroutines, memory, GC, etc.).
	Runtime *metrics.RuntimeMetrics

	// System provides system-level metrics (CPU, disk, network).
	// May be nil if DisableSystemMetrics is true.
	System *metrics.SystemMetrics
}

// InitOptions configures optional behaviors during observability stack initialization.
type InitOptions struct {
	// DisableSystemMetrics prevents system metrics collection if true.
	// This is useful in containerized environments where system metrics
	// may not be meaningful or accessible.
	DisableSystemMetrics bool

	// SystemDiskPath specifies the disk path to monitor for disk metrics.
	// Defaults to "/" if not specified.
	SystemDiskPath string
}

// Observability manages the lifecycle of the observability stack components.
//
// This is a lower-level type used internally by Stack. Most applications should
// use Init() to create a Stack rather than working with Observability directly.
type Observability struct {
	config       config.Config
	log          Logger
	cleanupFuncs []func(context.Context) error
	initialized  bool
	meter        metric.Meter
}

// Init initializes the complete observability stack automatically.
//
// This is the main entry point for adding observability to your application.
// It loads configuration from environment variables, initializes all enabled
// components, and returns a ready-to-use Stack with pre-configured metrics.
//
// Init will return an error if:
//   - Required environment variables are missing
//   - The OTLP endpoint cannot be reached (if metrics are enabled)
//   - Metrics initialization fails
//
// Example:
//
//	stack, err := observability.Init(log, nil)
//	if err != nil {
//	    log.Fatal("failed to init observability", zap.Error(err))
//	}
//	defer func() {
//		if err := observability.Shutdown(context.Background()); err != nil {
//			log.Error("Failed to shutdown observability", zap.Error(err))
//		}
//	}()
//
//	// Use pre-configured metrics
//	stack.HTTP.RecordRequest(ctx, "GET", "/api/users", 200, duration, reqSize, respSize)
func Init(log Logger, opts *InitOptions) (*Stack, error) {
	if opts == nil {
		opts = &InitOptions{SystemDiskPath: "/"}
	}
	if opts.SystemDiskPath == "" {
		opts.SystemDiskPath = "/"
	}

	log.Info("Initializing observability stack")

	// Load configuration from environment
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Create and start observability
	obs, err := newWithConfig(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create observability: %w", err)
	}

	ctx := context.Background()
	if err := obs.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start observability: %w", err)
	}

	stack := &Stack{obs: obs, log: log}

	// Initialize all metrics automatically if enabled
	if cfg.MetricsEnabled {
		if err := stack.initializeMetrics(opts); err != nil {
			if shutdownErr := obs.Shutdown(ctx); shutdownErr != nil {
				// Log the shutdown error but return the original initialization error
				log.Error("failed to shutdown observability after initialization error",
					zap.Error(shutdownErr))
			}
			return nil, fmt.Errorf("failed to initialize metrics: %w", err)
		}
	}

	log.Info("Observability stack initialized successfully",
		zap.Bool("metrics", cfg.MetricsEnabled),
		zap.Bool("tracing", cfg.TracingEnabled),
		zap.Bool("logs", cfg.LogsEnabled),
		zap.Bool("profiling", cfg.ProfilingEnabled))

	return stack, nil
}

// MustInit is like Init but panics if initialization fails.
//
// This is useful for simplifying application startup when an observability
// initialization failure should terminate the application.
//
// Example:
//
//	func main() {
//	    log := logger.Get()
//	    stack := observability.MustInit(log, nil)
//	    defer stack.Shutdown(context.Background())
//	    // ...
//	}
func MustInit(log Logger, opts *InitOptions) *Stack {
	stack, err := Init(log, opts)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize observability stack: %v", err))
	}
	return stack
}

// New creates a new Observability instance by loading configuration from environment variables.
//
// This is a lower-level function compared to Init(). It requires manual calls to Start()
// and metric initialization. For most use cases, Init() is preferred.
//
// Example:
//
//	obs, err := observability.New(log)
//	if err != nil {
//	    return err
//	}
//	if err := obs.Start(ctx); err != nil {
//	    return err
//	}
//	// Manually initialize metrics as needed
func New(log Logger) (*Observability, error) {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load config from environment: %w", err)
	}
	return newWithConfig(cfg, log)
}

// MustNew creates a new Observability instance or panics on error.
//
// This is the panic-on-error variant of New().
func MustNew(log Logger) *Observability {
	obs, err := New(log)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize observability: %v", err))
	}
	return obs
}

// Start initializes all enabled components of the observability stack.
//
// Start must be called before using any observability features. It will return
// an error if called multiple times or if component initialization fails.
//
// If no components are enabled in the configuration, Start succeeds immediately
// and logs a message indicating the stack is disabled.
func (o *Observability) Start(ctx context.Context) error {
	if o.initialized {
		return fmt.Errorf("observability already initialized")
	}

	if !o.config.IsEnabled() {
		o.log.Info("Observability stack disabled - no components enabled")
		o.initialized = true
		return nil
	}

	o.log.Info("Starting observability stack",
		zap.Strings("components", o.config.EnabledComponents()))

	startTime := time.Now()

	if o.config.MetricsEnabled {
		if err := o.initMetrics(ctx); err != nil {
			return fmt.Errorf("failed to initialize metrics: %w", err)
		}
	}

	// TODO: Initialize Tracing (future)
	// TODO: Initialize Logs (future)
	// TODO: Initialize Profiling (future)

	o.log.Info("Observability stack started successfully",
		zap.Duration("duration", time.Since(startTime)),
		zap.Int("components", len(o.cleanupFuncs)))

	o.initialized = true
	return nil
}

// Shutdown performs graceful shutdown of the observability stack.
//
// Shutdown flushes any pending metrics and cleans up resources. It should be
// called during application shutdown, typically in a defer statement.
//
// The provided context controls the shutdown timeout. If the context expires
// before shutdown completes, an error is returned but cleanup continues for
// remaining components.
//
// Example:
//
//	stack, _ := observability.Init(log, nil)
//	defer stack.Shutdown(context.Background())
func (s *Stack) Shutdown(ctx context.Context) error {
	return s.obs.Shutdown(ctx)
}

// Shutdown performs graceful shutdown of all observability components.
//
// This method ensures metrics are flushed and resources are properly cleaned up.
// It will attempt to shut down all components even if some fail, and will return
// an error describing all failures that occurred.
//
// If Shutdown is called on an uninitialized Observability instance, it returns
// immediately without error.
func (o *Observability) Shutdown(ctx context.Context) error {
	if !o.initialized {
		o.log.Debug("Observability not initialized, skipping shutdown")
		return nil
	}

	if len(o.cleanupFuncs) == 0 {
		o.log.Debug("No cleanup functions registered")
		return nil
	}

	o.log.Info("Shutting down observability stack",
		zap.Int("components", len(o.cleanupFuncs)))

	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var errors []error
	for i, cleanup := range o.cleanupFuncs {
		o.log.Debug("Shutting down component",
			zap.Int("index", i+1),
			zap.Int("total", len(o.cleanupFuncs)))

		if err := cleanup(shutdownCtx); err != nil {
			o.log.Error("Failed to shutdown component",
				zap.Int("index", i+1),
				zap.Error(err))
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		o.log.Error("Observability shutdown completed with errors",
			zap.Int("error_count", len(errors)))
		return fmt.Errorf("shutdown had %d errors: %v", len(errors), errors)
	}

	o.log.Info("Observability stack shutdown complete")
	o.initialized = false
	return nil
}

// newWithConfig creates a new Observability instance with the provided configuration.
//
// This internal constructor validates the configuration and initializes the
// Observability struct. It does not start any components.
func newWithConfig(cfg config.Config, log Logger) (*Observability, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &Observability{
		config:       cfg,
		log:          log,
		cleanupFuncs: make([]func(context.Context) error, 0),
		initialized:  false,
	}, nil
}

// initMetrics initializes the OpenTelemetry metrics system.
//
// This method creates an OTLP gRPC exporter, configures the meter provider with
// service identification attributes, and registers cleanup functions for graceful
// shutdown.
func (o *Observability) initMetrics(ctx context.Context) error {
	o.log.Debug("Initializing metrics exporter",
		zap.String("endpoint", o.config.OTLPEndpoint),
		zap.Int("export_interval_sec", o.config.MetricExportIntervalSec))

	// Create OTLP gRPC exporter
	exporter, err := otlpmetricgrpc.New(
		ctx,
		otlpmetricgrpc.WithEndpoint(o.config.OTLPEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	// Create resource with service identification
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceName(o.config.ServiceName),
			semconv.ServiceVersion(o.config.ServiceVersion),
			semconv.DeploymentEnvironment(o.config.Environment),
			semconv.HostName(o.config.HostName),
		),
		resource.WithAttributes(
			attribute.String("deployment.id", o.config.DeploymentID),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Create meter provider
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			exporter,
			sdkmetric.WithInterval(time.Duration(o.config.MetricExportIntervalSec)*time.Second),
		)),
		sdkmetric.WithResource(res),
	)

	// Set global meter provider
	otel.SetMeterProvider(provider)

	// Create meter instance
	o.meter = provider.Meter(o.config.ServiceName)

	// Register cleanup
	o.cleanupFuncs = append(o.cleanupFuncs, func(ctx context.Context) error {
		o.log.Debug("Shutting down meter provider")
		if err := provider.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown meter provider: %w", err)
		}
		o.log.Debug("Meter provider shutdown complete")
		return nil
	})

	o.log.Info("Metrics initialized",
		zap.String("endpoint", o.config.OTLPEndpoint),
		zap.String("service", o.config.ServiceName))

	return nil
}

// initializeMetrics initializes all metric collectors automatically.
//
// This method creates HTTP, database, cache, runtime, and optionally system
// metrics collectors. It is called internally by Init() when metrics are enabled.
func (s *Stack) initializeMetrics(opts *InitOptions) error {
	meter := s.obs.Meter()

	s.log.Debug("Initializing HTTP metrics")
	httpMetrics, err := metrics.NewHTTPMetrics(meter)
	if err != nil {
		return fmt.Errorf("failed to create HTTP metrics: %w", err)
	}
	s.HTTP = httpMetrics

	s.log.Debug("Initializing database metrics")
	dbMetrics, err := metrics.NewDBMetrics(meter)
	if err != nil {
		return fmt.Errorf("failed to create DB metrics: %w", err)
	}
	s.DB = dbMetrics

	s.log.Debug("Initializing cache metrics")
	cacheMetrics, err := metrics.NewCacheMetrics(meter)
	if err != nil {
		return fmt.Errorf("failed to create cache metrics: %w", err)
	}
	s.Cache = cacheMetrics

	s.log.Debug("Initializing runtime metrics")
	runtimeMetrics, err := metrics.NewRuntimeMetrics(meter)
	if err != nil {
		return fmt.Errorf("failed to create runtime metrics: %w", err)
	}
	s.Runtime = runtimeMetrics
	s.log.Info("Runtime metrics initialized",
		zap.Int("goroutines", metrics.NumGoroutines()),
		zap.Float64("memory_mb", metrics.MemoryUsageMB()))

	// System metrics (optional)
	if !opts.DisableSystemMetrics {
		s.log.Debug("Initializing system metrics",
			zap.String("disk_path", opts.SystemDiskPath))
		systemMetrics, err := metrics.NewSystemMetrics(meter, metrics.SystemMetricsConfig{
			DiskPath: opts.SystemDiskPath,
		})
		if err != nil {
			return fmt.Errorf("failed to create system metrics: %w", err)
		}
		s.System = systemMetrics
		s.log.Info("System metrics initialized")
	} else {
		s.log.Info("System metrics disabled")
	}

	return nil
}

// Meter returns the global OpenTelemetry meter for creating custom metric instruments.
//
// This method panics if observability is not initialized or metrics are not enabled.
// For typical use cases, use the pre-configured metrics in Stack (HTTP, DB, Cache, etc.)
// rather than creating custom metrics.
//
// Example:
//
//	meter := obs.Meter()
//	counter, _ := meter.Int64Counter("custom.requests")
//	counter.Add(ctx, 1)
func (o *Observability) Meter() metric.Meter {
	if o.meter == nil {
		panic("observability not initialized or metrics not enabled")
	}
	return o.meter
}

// IsInitialized returns true if the observability stack has been initialized.
func (o *Observability) IsInitialized() bool {
	return o.initialized
}

// Config returns the current observability configuration.
func (o *Observability) Config() config.Config {
	return o.config
}

// Config returns the current observability configuration.
func (s *Stack) Config() config.Config {
	return s.obs.Config()
}

// IsInitialized returns true if the stack has been initialized.
func (s *Stack) IsInitialized() bool {
	return s.obs.IsInitialized()
}

// Meter returns the OpenTelemetry meter for creating advanced custom metrics.
//
// For most use cases, the pre-configured metrics (HTTP, DB, Cache, Runtime, System)
// are sufficient. Use this method only when you need to create custom metric
// instruments that aren't covered by the built-in metrics.
//
// Example:
//
//	meter := stack.Meter()
//	customCounter, _ := meter.Int64Counter("app.custom.operations")
//	customCounter.Add(ctx, 1, metric.WithAttributes(
//	    attribute.String("operation", "process"),
//	))
func (s *Stack) Meter() metric.Meter {
	return s.obs.Meter()
}

// HTTPMetricsMiddleware returns a Chi-compatible middleware that automatically
// records HTTP metrics for all requests.
//
// The middleware tracks request count, duration, request/response sizes, and
// groups metrics by method, path, and status code. It integrates seamlessly
// with the go-chi/chi router.
//
// This is a convenience wrapper around HTTPMetrics.Middleware() for easy access
// from the Stack.
//
// Example:
//
//	stack, _ := observability.Init(log, nil)
//	router := chi.NewRouter()
//	router.Use(stack.HTTPMetricsMiddleware())
//	router.Get("/api/users", handler)
func (s *Stack) HTTPMetricsMiddleware() func(http.Handler) http.Handler {
	return s.HTTP.Middleware()
}
