// cmd/example/main.go
//
// Complete example application using the observability package
package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	observability "github.com/gath-stack/gobservability"
	logger "github.com/gath-stack/gologger"
)

func main() {
	// Load .env file if present (optional in production, required in development)
	// In production, use actual environment variables set by the deployment system
	if err := godotenv.Load(); err != nil {
		// Not fatal - in production we expect real env vars
		fmt.Fprintf(os.Stderr, "Warning: .env file not found or could not be loaded (this is normal in production)\n")
	}

	// Initialize logger FIRST - fail fast if configuration is invalid
	// This uses the strict FromEnv() which requires all environment variables
	logCfg, err := logger.FromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Invalid logger configuration: %v\n", err)
		fmt.Fprintf(os.Stderr, "Required environment variables: LOG_LEVEL, APP_ENV, APP_NAME\n")
		os.Exit(1)
	}

	if err := logger.InitGlobal(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	// Ensure logs are flushed before exit
	defer func() {
		if err := logger.Get().Sync(); err != nil {
			// Logger sync errors on stdout/stderr are often harmless on some systems
			// but we log them anyway for visibility
			fmt.Fprintf(os.Stderr, "Warning: failed to sync logger: %v\n", err)
		}
	}()

	log := logger.Get()

	// ========================================
	// 2. Initialize Observability
	// ========================================
	observability, err := observability.Init(log, nil)
	if err != nil {
		log.Fatal("Failed to initialize observability", zap.Error(err))
	}
	defer func() {
		if err := observability.Shutdown(context.Background()); err != nil {
			log.Error("Failed to shutdown observability", zap.Error(err))
		}
	}()

	log.Info("Observability initialized",
		zap.Strings("components", observability.Config().EnabledComponents()))

	// ========================================
	// 3. Setup HTTP Server
	// ========================================
	app := &Application{
		log:      log,
		obsStack: observability,
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
		log.Info("HTTP server starting", zap.String("addr", server.Addr))
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

	// Base middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(app.loggingMiddleware)

	// Observability middleware (if metrics enabled)
	if app.obsStack.Config().MetricsEnabled {
		r.Use(app.obsStack.HTTPMetricsMiddleware())
	}

	// ========================================
	// Routes
	// ========================================

	r.Get("/health", app.healthHandler)
	r.Get("/ready", app.readyHandler)

	r.Route("/api", func(r chi.Router) {
		r.Get("/", app.apiIndexHandler)

		// Database operations
		r.Route("/users", func(r chi.Router) {
			r.Use(app.dbConnectionMiddleware)
			r.Get("/", app.listUsersHandler)
			r.Post("/", app.createUserHandler)
			r.Get("/{id}", app.getUserHandler)
			r.Put("/{id}", app.updateUserHandler)
			r.Delete("/{id}", app.deleteUserHandler)
		})

		// Cache operations
		r.Route("/cache", func(r chi.Router) {
			r.Get("/{key}", app.cacheGetHandler)
			r.Post("/{key}", app.cacheSetHandler)
			r.Delete("/{key}", app.cacheDeleteHandler)
		})

		// Test endpoints
		r.Get("/slow", app.slowHandler)
		r.Get("/error", app.errorHandler)
		r.Get("/random", app.randomHandler)
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

		// Increments connections
		app.obsStack.DB.UpdateConnections(ctx, +1)

		// Decrement connections
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
			"/api/random"
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
			"otlp_endpoint": "%s"
		}
	}`,
		app.obsStack.IsInitialized(),
		components,
		cfg.ServiceName,
		cfg.ServiceVersion,
		cfg.Environment,
		cfg.OTLPEndpoint)
}

// ============================================================================
// Handlers - Database Operations
// ============================================================================

func (app *Application) listUsersHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Simulate database query
	start := time.Now()
	users := []string{"alice", "bob", "charlie", "dave", "eve"}
	time.Sleep(time.Duration(10+rand.Intn(40)) * time.Millisecond)
	duration := time.Since(start)

	// Record metrics
	app.obsStack.DB.RecordQuery(ctx, "SELECT", "users", duration, true)

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
	time.Sleep(time.Duration(20+rand.Intn(30)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "INSERT", "users", duration, true)

	app.log.Info("User created", zap.Duration("db_duration", duration))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"id":"123","username":"newuser"}`)
}

func (app *Application) getUserHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	start := time.Now()
	time.Sleep(time.Duration(5+rand.Intn(15)) * time.Millisecond)
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
	time.Sleep(time.Duration(15+rand.Intn(25)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "UPDATE", "users", duration, true)

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
	time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "DELETE", "users", duration, true)

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
	hit := rand.Float32() > 0.3 // 70% hit rate
	time.Sleep(time.Duration(1+rand.Intn(9)) * time.Millisecond)
	duration := time.Since(start)

	var size int64
	if hit {
		size = int64(100 + rand.Intn(900))
	}

	app.obsStack.Cache.RecordGet(ctx, "get", hit, duration, size)

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
	size := int64(100 + rand.Intn(900))
	success := rand.Float32() > 0.05 // 95% success rate
	time.Sleep(time.Duration(2+rand.Intn(8)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.Cache.RecordSet(ctx, duration, size, success)

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
	success := rand.Float32() > 0.05 // 95% success rate
	time.Sleep(time.Duration(1+rand.Intn(5)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.Cache.RecordDelete(ctx, duration, success)

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
	delay := 2000 + rand.Intn(1000)
	time.Sleep(time.Duration(delay) * time.Millisecond)

	app.log.Warn("Slow operation completed", zap.Int("delay_ms", delay))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"message":"slow operation completed","delay_ms":%d}`, delay)
}

func (app *Application) errorHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	start := time.Now()
	time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond)
	duration := time.Since(start)

	// Simulate failed database query
	app.obsStack.DB.RecordQuery(ctx, "SELECT", "users", duration, false)

	app.log.Error("Simulated error occurred",
		zap.String("error_type", "database_error"))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `{"error":"database connection failed"}`)
}

func (app *Application) randomHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	success := rand.Float32() > 0.2 // 80% success rate

	start := time.Now()
	time.Sleep(time.Duration(10+rand.Intn(90)) * time.Millisecond)
	duration := time.Since(start)

	app.obsStack.DB.RecordQuery(ctx, "SELECT", "data", duration, success)

	if success {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"result":"success","data":"random_data_%d"}`, rand.Intn(1000))
	} else {
		app.log.Error("Random error occurred")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":"random error occurred"}`)
	}
}
