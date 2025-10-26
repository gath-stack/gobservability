// cmd/example/main.go
//
// # Complete example application using the observability package with both metrics and tracing
//
// This example demonstrates:
//   - Automatic HTTP metrics and distributed tracing via unified middleware
//   - Manual DB operation instrumentation (metrics + tracing)
//   - Cache operation instrumentation (metrics + tracing)
//   - Custom span creation for business logic
//   - Error handling and span status management
//   - Graceful shutdown with proper cleanup
//
// Environment Variables Required:
//   - APP_NAME: Service name (e.g., "example-api")
//   - APP_VERSION: Service version (e.g., "1.0.0")
//   - APP_ENV: Environment (development, staging, production)
//   - LOG_LEVEL: Logging level (debug, info, warn, error)
//   - OBSERVABILITY_METRICS_ENABLED: Enable metrics collection (true/false)
//   - OBSERVABILITY_TRACING_ENABLED: Enable distributed tracing (true/false)
//   - OBSERVABILITY_OTLP_ENDPOINT: OTLP collector endpoint (e.g., "localhost:4317")
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	observability "github.com/gath-stack/gobservability"
	logger "github.com/gath-stack/gologger"
)

func main() {
	// ========================================
	// 1. Initialize Logger
	// ========================================
	logger.MustInitFromEnv()
	log := logger.Get()
	defer func() {
		if err := logger.Get().Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "logger sync error: %v\n", err)
		}
	}()

	// ========================================
	// 2. Initialize Observability Stack
	// ========================================
	// Init() automatically configures both metrics and tracing based on environment variables.
	// The stack provides:
	//   - HTTP metrics and tracing (via unified middleware)
	//   - DB operation instrumentation
	//   - Cache operation instrumentation
	//   - Runtime metrics (automatic)
	//   - System metrics (automatic, optional)
	obsStack, err := observability.Init(log, nil)
	if err != nil {
		log.Fatal("Failed to initialize observability", zap.Error(err))
	}
	defer func() {
		// Graceful shutdown ensures all pending metrics and traces are flushed
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := obsStack.Shutdown(shutdownCtx); err != nil {
			log.Error("Failed to shutdown observability", zap.Error(err))
		}
	}()

	if obsStack.Config().LogsEnabled && obsStack.Logs != nil {
		if err := obsStack.EnableLogsExport(log); err != nil {
			log.Error("Failed to enable logs export", zap.Error(err))
		} else {
			log.Info("Logs export to Loki is ACTIVE")
		}
	}

	log.Info("Observability initialized",
		zap.Strings("components", obsStack.Config().EnabledComponents()),
		zap.Bool("metrics", obsStack.Config().MetricsEnabled),
		zap.Bool("tracing", obsStack.Config().TracingEnabled),
		zap.Bool("logs", obsStack.Config().LogsEnabled))

	// ========================================
	// 3. Setup HTTP Server
	// ========================================
	app := &Application{
		log:      log,
		obsStack: obsStack,
	}

	router := app.setupRouter()

	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ========================================
	// 4. Start HTTP Server
	// ========================================
	serverErrors := make(chan error, 1)
	go func() {
		log.Info("HTTP server starting",
			zap.String("addr", server.Addr),
			zap.String("service", obsStack.Config().ServiceName),
			zap.String("version", obsStack.Config().ServiceVersion))
		serverErrors <- server.ListenAndServe()
	}()

	// ========================================
	// 5. Wait for shutdown signal
	// ========================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Fatal("HTTP server failed", zap.Error(err))
	case sig := <-quit:
		log.Info("Received shutdown signal", zap.String("signal", sig.String()))
	}

	// ========================================
	// 6. Graceful Shutdown
	// ========================================
	log.Info("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("Server forced to shutdown", zap.Error(err))
		server.Close()
	}

	log.Info("Server exited gracefully")
}

// ============================================================================
// Application struct
// ============================================================================

type Application struct {
	log      *logger.Logger
	obsStack *observability.Stack
}

// ============================================================================
// Router Setup
// ============================================================================

func (app *Application) setupRouter() *chi.Mux {
	r := chi.NewRouter()

	// ========================================
	// Base Middleware Stack
	// ========================================
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(app.loggingMiddleware)

	// ========================================
	// Observability Middleware
	// ========================================
	// IMPORTANT: We use HTTPObservabilityMiddleware() which combines both metrics and tracing
	// in a single pass. This is more efficient than stacking separate middlewares.
	//
	// The middleware automatically:
	//   - Creates a root span for each HTTP request (if tracing enabled)
	//   - Records HTTP metrics (duration, status, bytes) (if metrics enabled)
	//   - Propagates trace context to downstream operations via ctx
	//   - Marks spans as error when status >= 400
	//
	// Alternative approaches (less recommended):
	//   r.Use(app.obsStack.HTTPMetricsMiddleware())  // Only metrics
	//   r.Use(app.obsStack.Tracing.HTTP.Middleware()) // Only tracing
	r.Use(app.obsStack.HTTPObservabilityMiddleware())

	// ========================================
	// Routes
	// ========================================

	// Health check endpoints (no observability overhead)
	r.Get("/health", app.healthHandler)
	r.Get("/ready", app.readyHandler)

	r.Route("/api", func(r chi.Router) {
		r.Get("/", app.apiIndexHandler)

		// Database operations - demonstrates DB metrics + tracing
		r.Route("/users", func(r chi.Router) {
			r.Use(app.dbConnectionMiddleware)
			r.Get("/", app.listUsersHandler)
			r.Post("/", app.createUserHandler)
			r.Get("/{id}", app.getUserHandler)
			r.Put("/{id}", app.updateUserHandler)
			r.Delete("/{id}", app.deleteUserHandler)
		})

		// Cache operations - demonstrates cache metrics + tracing
		r.Route("/cache", func(r chi.Router) {
			r.Get("/{key}", app.cacheGetHandler)
			r.Post("/{key}", app.cacheSetHandler)
			r.Delete("/{key}", app.cacheDeleteHandler)
		})

		// Test endpoints
		r.Get("/slow", app.slowHandler)       // Tests slow operation tracing
		r.Get("/error", app.errorHandler)     // Tests error handling
		r.Get("/random", app.randomHandler)   // Tests variable success rate
		r.Get("/complex", app.complexHandler) // Tests custom span creation
	})

	r.Get("/metrics/info", app.metricsInfoHandler)

	return r
}

// ============================================================================
// Middleware
// ============================================================================

func (app *Application) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		duration := time.Since(start)

		app.log.Info("HTTP request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", ww.Status()),
			zap.Duration("duration", duration),
			zap.Int("bytes", ww.BytesWritten()),
			zap.String("remote_addr", r.RemoteAddr))
	})
}

func (app *Application) dbConnectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Track active DB connections (metrics only)
		app.obsStack.DB.UpdateConnections(ctx, +1)
		defer app.obsStack.DB.UpdateConnections(ctx, -1)

		next.ServeHTTP(w, r)
	})
}

// ============================================================================
// Handlers - Health & Info
// ============================================================================

func (app *Application) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","timestamp":"%s"}`, time.Now().Format(time.RFC3339))
}

func (app *Application) readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ready := app.obsStack.IsInitialized()

	if ready {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ready","observability":true}`)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"status":"not_ready","observability":false}`)
	}
}

func (app *Application) apiIndexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{
		"service": "%s",
		"version": "%s",
		"environment": "%s",
		"endpoints": [
			"/api/users",
			"/api/cache",
			"/api/slow",
			"/api/error",
			"/api/random",
			"/api/complex"
		]
	}`,
		os.Getenv("APP_NAME"),
		os.Getenv("APP_VERSION"),
		os.Getenv("APP_ENV"))
}

func (app *Application) metricsInfoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	cfg := app.obsStack.Config()
	components := fmt.Sprintf(`["%s"]`, "")
	if len(cfg.EnabledComponents()) > 0 {
		componentsStr := ""
		for i, comp := range cfg.EnabledComponents() {
			if i > 0 {
				componentsStr += ","
			}
			componentsStr += fmt.Sprintf(`"%s"`, comp)
		}
		components = fmt.Sprintf(`[%s]`, componentsStr)
	}

	fmt.Fprintf(w, `{
		"observability": {
			"initialized": %v,
			"components": %s,
			"service_name": "%s",
			"service_version": "%s",
			"environment": "%s",
			"otlp_endpoint": "%s",
			"tracing_enabled": %v,
			"metrics_enabled": %v,
			"sampling_rate": %.2f
		}
	}`,
		app.obsStack.IsInitialized(),
		components,
		cfg.ServiceName,
		cfg.ServiceVersion,
		cfg.Environment,
		cfg.OTLPEndpoint,
		cfg.TracingEnabled,
		cfg.MetricsEnabled,
		cfg.TraceSamplingRate)
}

// ============================================================================
// Handlers - Database Operations
// ============================================================================

func (app *Application) listUsersHandler(w http.ResponseWriter, r *http.Request) {
	// Get context from request - it already contains the root HTTP span from middleware
	ctx := r.Context()

	// Simulate database query with BOTH metrics and tracing
	start := time.Now()

	// Create a child span for the DB operation (if tracing is enabled)
	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "SELECT", "users")
		defer dbSpan.End()
	}

	// Simulate query execution
	users := []string{"alice", "bob", "charlie", "dave", "eve"}
	time.Sleep(time.Duration(10+cryptoRandIntn(40)) * time.Millisecond)
	duration := time.Since(start)

	// Record DB metrics (always, if metrics enabled)
	app.obsStack.DB.RecordQuery(ctx, "SELECT", "users", duration, true)

	// Add custom attributes to the span (if tracing enabled)
	if app.obsStack.Config().TracingEnabled && dbSpan != nil {
		dbSpan.SetAttributes(attribute.Int("db.rows_returned", len(users)))
	}

	app.log.Debug("Listed users",
		zap.Int("count", len(users)),
		zap.Duration("db_duration", duration))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"users":["%s","%s","%s","%s","%s"]}`, users[0], users[1], users[2], users[3], users[4])
}

func (app *Application) createUserHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	start := time.Now()

	// Create DB span
	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "INSERT", "users")
		defer dbSpan.End()
	}

	time.Sleep(time.Duration(20+cryptoRandIntn(30)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "INSERT", "users", duration, true)

	if app.obsStack.Config().TracingEnabled && dbSpan != nil {
		dbSpan.SetAttributes(attribute.Int64("db.rows_affected", 1))
	}

	app.log.Info("User created", zap.Duration("db_duration", duration))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"id":"123","username":"newuser"}`)
}

func (app *Application) getUserHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	start := time.Now()

	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "SELECT", "users")
		dbSpan.SetAttributes(attribute.String("user.id", userID))
		defer dbSpan.End()
	}

	time.Sleep(time.Duration(5+cryptoRandIntn(15)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "SELECT", "users", duration, true)

	app.log.Debug("User retrieved",
		zap.String("user_id", userID),
		zap.Duration("db_duration", duration))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"id":"%s","username":"user_%s"}`, userID, userID)
}

func (app *Application) updateUserHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	start := time.Now()

	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "UPDATE", "users")
		dbSpan.SetAttributes(attribute.String("user.id", userID))
		defer dbSpan.End()
	}

	time.Sleep(time.Duration(15+cryptoRandIntn(25)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "UPDATE", "users", duration, true)

	if app.obsStack.Config().TracingEnabled && dbSpan != nil {
		dbSpan.SetAttributes(attribute.Int64("db.rows_affected", 1))
	}

	app.log.Info("User updated",
		zap.String("user_id", userID),
		zap.Duration("db_duration", duration))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"id":"%s","username":"updated_user"}`, userID)
}

func (app *Application) deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	start := time.Now()

	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "DELETE", "users")
		dbSpan.SetAttributes(attribute.String("user.id", userID))
		defer dbSpan.End()
	}

	time.Sleep(time.Duration(10+cryptoRandIntn(20)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "DELETE", "users", duration, true)

	if app.obsStack.Config().TracingEnabled && dbSpan != nil {
		dbSpan.SetAttributes(attribute.Int64("db.rows_affected", 1))
	}

	app.log.Info("User deleted",
		zap.String("user_id", userID),
		zap.Duration("db_duration", duration))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"message":"user deleted"}`)
}

// ============================================================================
// Handlers - Cache Operations
// ============================================================================

func (app *Application) cacheGetHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := chi.URLParam(r, "key")

	start := time.Now()
	hit := cryptoRandFloat32() > 0.3 // 70% hit rate

	// Create cache span
	var cacheSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, cacheSpan = app.obsStack.Tracing.Cache.StartSpan(ctx, "GET", key)
		defer cacheSpan.End()
	}

	time.Sleep(time.Duration(1+cryptoRandIntn(9)) * time.Millisecond)
	duration := time.Since(start)

	var size int64
	if hit {
		size = int64(100 + cryptoRandIntn(900))
	}

	// Record metrics
	app.obsStack.Cache.RecordGet(ctx, "get", hit, duration, size)

	// Update span with hit/miss info
	if app.obsStack.Config().TracingEnabled && cacheSpan != nil {
		app.obsStack.Tracing.Cache.RecordHit(cacheSpan, hit)
		if hit && size > 0 {
			app.obsStack.Tracing.Cache.SetItemSize(cacheSpan, size)
		}
	}

	if hit {
		app.log.Debug("Cache hit",
			zap.String("key", key),
			zap.Duration("duration", duration))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"key":"%s","value":"cached_value_%s","hit":true}`, key, key)
	} else {
		app.log.Debug("Cache miss",
			zap.String("key", key),
			zap.Duration("duration", duration))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"key":"%s","hit":false}`, key)
	}
}

func (app *Application) cacheSetHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := chi.URLParam(r, "key")

	start := time.Now()
	size := int64(100 + cryptoRandIntn(900))
	success := cryptoRandFloat32() > 0.05 // 95% success rate

	// Use convenience method that handles both metrics and creates span automatically
	time.Sleep(time.Duration(2+cryptoRandIntn(8)) * time.Millisecond)
	duration := time.Since(start)

	// Record metrics
	app.obsStack.Cache.RecordSet(ctx, duration, size, success)

	// Record trace (if enabled)
	if app.obsStack.Config().TracingEnabled {
		var err error
		if !success {
			err = fmt.Errorf("cache set failed")
		}
		app.obsStack.Tracing.Cache.RecordSet(ctx, key, duration, err)
	}

	if success {
		app.log.Debug("Cache set",
			zap.String("key", key),
			zap.Duration("duration", duration))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"key":"%s","success":true}`, key)
	} else {
		app.log.Error("Cache set failed", zap.String("key", key))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"key":"%s","success":false,"error":"cache error"}`, key)
	}
}

func (app *Application) cacheDeleteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := chi.URLParam(r, "key")

	start := time.Now()
	success := cryptoRandFloat32() > 0.05 // 95% success rate
	time.Sleep(time.Duration(1+cryptoRandIntn(5)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.Cache.RecordDelete(ctx, duration, success)

	if app.obsStack.Config().TracingEnabled {
		var err error
		if !success {
			err = fmt.Errorf("cache delete failed")
		}
		app.obsStack.Tracing.Cache.RecordDelete(ctx, key, duration, err)
	}

	if success {
		app.log.Debug("Cache delete",
			zap.String("key", key),
			zap.Duration("duration", duration))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"key":"%s","deleted":true}`, key)
	} else {
		app.log.Error("Cache delete failed", zap.String("key", key))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"key":"%s","deleted":false,"error":"cache error"}`, key)
	}
}

// ============================================================================
// Handlers - Test Endpoints
// ============================================================================

func (app *Application) slowHandler(w http.ResponseWriter, r *http.Request) {
	delay := 2000 + cryptoRandIntn(1000)
	time.Sleep(time.Duration(delay) * time.Millisecond)

	app.log.Warn("Slow operation completed", zap.Int64("delay_ms", delay))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"message":"slow operation completed","delay_ms":%d}`, delay)
}

func (app *Application) errorHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	start := time.Now()

	// Create DB span that will fail
	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "SELECT", "users")
		defer dbSpan.End()
	}

	time.Sleep(time.Duration(10+cryptoRandIntn(20)) * time.Millisecond)
	duration := time.Since(start)

	// Simulate failed database query with error
	err := fmt.Errorf("database connection failed")
	app.obsStack.DB.RecordQuery(ctx, "SELECT", "users", duration, false)

	// Record error in span
	if app.obsStack.Config().TracingEnabled && dbSpan != nil {
		app.obsStack.Tracing.DB.RecordError(dbSpan, err)
	}

	app.log.Error("Simulated error occurred",
		zap.String("error_type", "database_error"),
		zap.Error(err))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `{"error":"database connection failed"}`)
}

func (app *Application) randomHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	success := cryptoRandFloat32() > 0.2 // 80% success rate

	start := time.Now()

	var dbSpan trace.Span
	if app.obsStack.Config().TracingEnabled {
		ctx, dbSpan = app.obsStack.Tracing.DB.StartSpan(ctx, "SELECT", "data")
		defer dbSpan.End()
	}

	time.Sleep(time.Duration(10+cryptoRandIntn(90)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "SELECT", "data", duration, success)

	if !success && app.obsStack.Config().TracingEnabled && dbSpan != nil {
		app.obsStack.Tracing.DB.RecordError(dbSpan, fmt.Errorf("random error"))
	}

	if success {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"result":"success","data":"random_data_%d"}`, cryptoRandIntn(1000))
	} else {
		app.log.Error("Random error occurred")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":"random error occurred"}`)
	}
}

// complexHandler demonstrates creating custom spans for business logic
func (app *Application) complexHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Create a custom span for complex business logic
	// This span is a child of the HTTP span created by the middleware
	if app.obsStack.Config().TracingEnabled {
		tracer := app.obsStack.Tracer()
		ctx, span := tracer.Start(ctx, "complex.processing")
		defer span.End()

		// Add custom attributes
		span.SetAttributes(
			attribute.String("processing.type", "multi_step"),
			attribute.Int("steps.total", 3),
		)

		// Step 1: Validate input (child span)
		ctx, validateSpan := tracer.Start(ctx, "complex.validate")
		time.Sleep(50 * time.Millisecond)
		validateSpan.SetAttributes(attribute.Bool("validation.passed", true))
		validateSpan.End()

		// Step 2: Process data (child span)
		ctx, processSpan := tracer.Start(ctx, "complex.process")
		time.Sleep(100 * time.Millisecond)
		processSpan.SetAttributes(
			attribute.Int("items.processed", 42),
			attribute.String("algorithm", "custom_v2"),
		)
		processSpan.End()

		// Step 3: Save results (child span using DB tracing)
		start := time.Now()
		ctx, dbSpan := app.obsStack.Tracing.DB.StartSpan(ctx, "INSERT", "results")
		time.Sleep(75 * time.Millisecond)
		duration := time.Since(start)
		dbSpan.End()

		// Record DB metrics
		app.obsStack.DB.RecordQuery(ctx, "INSERT", "results", duration, true)

		// Mark main span as successful
		span.SetAttributes(attribute.Bool("processing.success", true))

		app.log.Info("Complex processing completed",
			zap.Duration("total_duration", time.Since(start)))
	} else {
		// Fallback when tracing is disabled
		time.Sleep(225 * time.Millisecond)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"result":"complex processing completed","steps":3}`)
}

// ============================================================================
// Helper Functions
// ============================================================================

// cryptoRandIntn returns a cryptographically secure random int64 in [0, n)
func cryptoRandIntn(n int64) int64 {
	r, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		panic(err)
	}
	return r.Int64()
}

// cryptoRandFloat32 returns a cryptographically secure random float32 in [0.0, 1.0)
func cryptoRandFloat32() float32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	u := binary.BigEndian.Uint32(b[:])
	return float32(u) / float32(math.MaxUint32)
}
