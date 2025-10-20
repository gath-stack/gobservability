// Package config handles all configuration loading and validation for the observability stack.
//
// Configuration is strictly environment-based with fail-fast validation.
// All settings are loaded from environment variables with sensible defaults where appropriate.
//
// # Environment Variables
//
// Required variables:
//   - APP_NAME: Service name for identification
//   - APP_VERSION: Service version (e.g., "1.0.0", "v2.3.1")
//   - APP_ENV: Environment (development, dev, local, staging, stage, test, production, prod)
//
// Feature flags (default: false):
//   - OBSERVABILITY_METRICS_ENABLED: Enable metrics collection
//   - OBSERVABILITY_TRACING_ENABLED: Enable distributed tracing
//   - OBSERVABILITY_LOGS_ENABLED: Enable structured logging export
//   - OBSERVABILITY_PROFILING_ENABLED: Enable continuous profiling
//
// Endpoints (required when features are enabled):
//   - OBSERVABILITY_OTLP_ENDPOINT: OTLP collector endpoint in host:port format
//   - OBSERVABILITY_PYROSCOPE_SERVER_ADDRESS: Pyroscope server address (for profiling)
//
// Optional configuration:
//   - DEPLOYMENT_ID: Unique deployment identifier
//   - HOSTNAME: Override system hostname
//   - OBSERVABILITY_METRIC_EXPORT_INTERVAL: Metrics export interval in seconds (default: 10)
//   - OBSERVABILITY_TRACE_SAMPLING_RATE: Trace sampling rate 0.0-1.0 (default: environment-based)
//   - OBSERVABILITY_METRIC_BATCH_SIZE: Metric batch size (default: 1024)
//   - OBSERVABILITY_TRACE_BATCH_SIZE: Trace batch size (default: 512)
//
// # Example Usage
//
//	// Load configuration with validation
//	cfg, err := config.LoadFromEnv()
//	if err != nil {
//	    log.Fatal("Invalid configuration", err)
//	}
//
//	// Check enabled components
//	if cfg.MetricsEnabled {
//	    fmt.Println("Metrics endpoint:", cfg.OTLPEndpoint)
//	}
//
//	// Or panic on error
//	cfg := config.MustLoadFromEnv()
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config defines the complete observability stack configuration.
//
// All fields are populated from environment variables during LoadFromEnv().
// The configuration enforces strict validation rules to ensure production safety.
type Config struct {
	// ServiceName identifies the application (from APP_NAME).
	ServiceName string

	// ServiceVersion is the application version (from APP_VERSION).
	ServiceVersion string

	// Environment specifies the deployment environment (from APP_ENV).
	// Valid values: development, dev, local, staging, stage, test, production, prod.
	Environment string

	// DeploymentID is an optional unique identifier for this deployment.
	DeploymentID string

	// HostName is the system hostname, auto-detected if not provided.
	HostName string

	// MetricsEnabled controls whether metrics collection is active.
	MetricsEnabled bool

	// TracingEnabled controls whether distributed tracing is active.
	TracingEnabled bool

	// LogsEnabled controls whether structured log export is active.
	LogsEnabled bool

	// ProfilingEnabled controls whether continuous profiling is active.
	ProfilingEnabled bool

	// OTLPEndpoint is the OTLP collector endpoint in host:port format.
	// Required when any of MetricsEnabled, TracingEnabled, or LogsEnabled is true.
	OTLPEndpoint string

	// PyroscopeServerAddress is the Pyroscope server address for profiling.
	// Required when ProfilingEnabled is true.
	PyroscopeServerAddress string

	// MetricExportIntervalSec is the interval in seconds between metric exports.
	// Valid range: 1-300. Default: 10.
	MetricExportIntervalSec int

	// TraceSamplingRate determines what fraction of traces to sample.
	// Valid range: 0.0-1.0. Default is environment-based:
	//   - development/staging: 1.0 (100%)
	//   - production: 0.1 (10%)
	TraceSamplingRate float64

	// MetricBatchSize is the maximum number of metrics per batch.
	// Default: 1024.
	MetricBatchSize int

	// TraceBatchSize is the maximum number of spans per batch.
	// Default: 512.
	TraceBatchSize int
}

// Common validation errors returned by LoadFromEnv and Validate.
var (
	// ErrMissingServiceName indicates APP_NAME is not set or empty.
	ErrMissingServiceName = errors.New("APP_NAME is required and cannot be empty")

	// ErrMissingServiceVersion indicates APP_VERSION is not set or empty.
	ErrMissingServiceVersion = errors.New("APP_VERSION is required and cannot be empty")

	// ErrMissingEnvironment indicates APP_ENV is not set or empty.
	ErrMissingEnvironment = errors.New("APP_ENV is required and cannot be empty")

	// ErrInvalidEnvironment indicates APP_ENV has an invalid value.
	ErrInvalidEnvironment = errors.New("APP_ENV must be one of: development, dev, local, staging, stage, test, production, prod")

	// ErrInvalidOTLPEndpoint indicates the OTLP endpoint format is invalid.
	ErrInvalidOTLPEndpoint = errors.New("OBSERVABILITY_OTLP_ENDPOINT must be in format host:port")

	// ErrMissingOTLPEndpoint indicates OTLP endpoint is required but not set.
	ErrMissingOTLPEndpoint = errors.New("OBSERVABILITY_OTLP_ENDPOINT is required when observability features are enabled")

	// ErrInvalidSamplingRate indicates trace sampling rate is out of valid range.
	ErrInvalidSamplingRate = errors.New("OBSERVABILITY_TRACE_SAMPLING_RATE must be between 0.0 and 1.0")

	// ErrMissingRequiredEnvVar indicates a required environment variable is missing.
	ErrMissingRequiredEnvVar = errors.New("required environment variable is not set")
)

// LoadFromEnv loads and validates observability configuration from environment variables.
//
// This function performs strict validation with fail-fast behavior. It will return
// an error if:
//   - Required variables (APP_NAME, APP_VERSION, APP_ENV) are missing or empty
//   - APP_ENV contains an invalid environment name
//   - Any observability feature is enabled but OBSERVABILITY_OTLP_ENDPOINT is missing
//   - OBSERVABILITY_OTLP_ENDPOINT is not in host:port format
//   - Numeric values are out of valid ranges
//
// The function applies sensible defaults for optional configuration values and
// automatically detects the hostname if not explicitly provided.
//
// Example:
//
//	cfg, err := config.LoadFromEnv()
//	if err != nil {
//	    log.Fatal("Configuration error:", err)
//	}
//
//	fmt.Printf("Service: %s v%s\n", cfg.ServiceName, cfg.ServiceVersion)
//	fmt.Printf("Enabled: %v\n", cfg.EnabledComponents())
func LoadFromEnv() (Config, error) {
	// 1. Load REQUIRED variables
	serviceName := os.Getenv("APP_NAME")
	if strings.TrimSpace(serviceName) == "" {
		return Config{}, ErrMissingServiceName
	}

	serviceVersion := os.Getenv("APP_VERSION")
	if strings.TrimSpace(serviceVersion) == "" {
		return Config{}, ErrMissingServiceVersion
	}

	environment := os.Getenv("APP_ENV")
	if strings.TrimSpace(environment) == "" {
		return Config{}, ErrMissingEnvironment
	}

	// Validate environment
	validEnvs := map[string]bool{
		"development": true,
		"dev":         true,
		"local":       true,
		"staging":     true,
		"stage":       true,
		"test":        true,
		"production":  true,
		"prod":        true,
	}
	if !validEnvs[strings.ToLower(environment)] {
		return Config{}, fmt.Errorf("%w: got '%s'", ErrInvalidEnvironment, environment)
	}

	// 2. Load feature flags (default: false)
	metricsEnabled := getEnvBool("OBSERVABILITY_METRICS_ENABLED", false)
	tracingEnabled := getEnvBool("OBSERVABILITY_TRACING_ENABLED", false)
	logsEnabled := getEnvBool("OBSERVABILITY_LOGS_ENABLED", false)
	profilingEnabled := getEnvBool("OBSERVABILITY_PROFILING_ENABLED", false)

	cfg := Config{
		ServiceName:             serviceName,
		ServiceVersion:          serviceVersion,
		Environment:             environment,
		DeploymentID:            getEnvString("DEPLOYMENT_ID", ""),
		MetricsEnabled:          metricsEnabled,
		TracingEnabled:          tracingEnabled,
		LogsEnabled:             logsEnabled,
		ProfilingEnabled:        profilingEnabled,
		OTLPEndpoint:            getEnvString("OBSERVABILITY_OTLP_ENDPOINT", ""),
		PyroscopeServerAddress:  getEnvString("OBSERVABILITY_PYROSCOPE_SERVER_ADDRESS", ""),
		MetricExportIntervalSec: getEnvInt("OBSERVABILITY_METRIC_EXPORT_INTERVAL", 10),
		TraceSamplingRate:       getEnvFloat("OBSERVABILITY_TRACE_SAMPLING_RATE", 0),
		MetricBatchSize:         getEnvInt("OBSERVABILITY_METRIC_BATCH_SIZE", 1024),
		TraceBatchSize:          getEnvInt("OBSERVABILITY_TRACE_BATCH_SIZE", 512),
	}

	// 3. Conditional validation: if any component is enabled, OTLP endpoint is required
	if cfg.MetricsEnabled || cfg.TracingEnabled || cfg.LogsEnabled {
		otlpEndpoint := os.Getenv("OBSERVABILITY_OTLP_ENDPOINT")
		if strings.TrimSpace(otlpEndpoint) == "" {
			return Config{}, ErrMissingOTLPEndpoint
		}

		if !isValidEndpoint(otlpEndpoint) {
			return Config{}, fmt.Errorf("%w: got '%s'", ErrInvalidOTLPEndpoint, otlpEndpoint)
		}
		cfg.OTLPEndpoint = otlpEndpoint
	}

	// 4. Apply defaults
	cfg = applyDefaults(cfg)

	// 5. Final validation
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// MustLoadFromEnv loads configuration from environment or panics on error.
//
// This is a convenience function for application startup when configuration
// errors should terminate the program. It's equivalent to calling LoadFromEnv()
// and panicking on error.
//
// Example:
//
//	func main() {
//	    cfg := config.MustLoadFromEnv()
//	    // Configuration is guaranteed to be valid here
//	    startServer(cfg)
//	}
func MustLoadFromEnv() Config {
	cfg, err := LoadFromEnv()
	if err != nil {
		panic(fmt.Sprintf("failed to load observability configuration from environment: %v", err))
	}
	return cfg
}

// Validate verifies that the configuration is internally consistent and complete.
//
// Validation checks include:
//   - Required fields (ServiceName, ServiceVersion, Environment) are non-empty
//   - If any component is enabled, appropriate endpoints are configured
//   - OTLP endpoint format is valid (host:port)
//   - Numeric values are within valid ranges
//   - Trace sampling rate is between 0.0 and 1.0
//   - Metric export interval is between 1 and 300 seconds
//
// If no components are enabled (IsEnabled() returns false), validation succeeds
// immediately, allowing the observability stack to be safely disabled.
//
// Returns nil if validation passes, or an error describing all validation failures.
func (c Config) Validate() error {
	var errors []string

	// Service identification
	if strings.TrimSpace(c.ServiceName) == "" {
		errors = append(errors, "ServiceName is required")
	}
	if strings.TrimSpace(c.ServiceVersion) == "" {
		errors = append(errors, "ServiceVersion is required")
	}
	if strings.TrimSpace(c.Environment) == "" {
		errors = append(errors, "Environment is required")
	}

	// If no components enabled, it's ok (silently disabled)
	if !c.IsEnabled() {
		return nil
	}

	// Validate endpoints if components are enabled
	if c.MetricsEnabled || c.TracingEnabled || c.LogsEnabled {
		if c.OTLPEndpoint == "" {
			errors = append(errors, "OBSERVABILITY_OTLP_ENDPOINT is required when metrics/tracing/logs are enabled")
		} else if !isValidEndpoint(c.OTLPEndpoint) {
			errors = append(errors, fmt.Sprintf("OBSERVABILITY_OTLP_ENDPOINT invalid format '%s' (expected host:port)", c.OTLPEndpoint))
		}
	}

	if c.ProfilingEnabled && c.PyroscopeServerAddress == "" {
		errors = append(errors, "OBSERVABILITY_PYROSCOPE_SERVER_ADDRESS is required when profiling is enabled")
	}

	// Validate ranges
	if c.TraceSamplingRate < 0.0 || c.TraceSamplingRate > 1.0 {
		errors = append(errors, fmt.Sprintf("OBSERVABILITY_TRACE_SAMPLING_RATE must be 0.0-1.0, got: %f", c.TraceSamplingRate))
	}

	if c.MetricExportIntervalSec < 1 || c.MetricExportIntervalSec > 300 {
		errors = append(errors, fmt.Sprintf("OBSERVABILITY_METRIC_EXPORT_INTERVAL must be 1-300, got: %d", c.MetricExportIntervalSec))
	}

	if len(errors) > 0 {
		return fmt.Errorf("observability configuration validation failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

// IsEnabled returns true if at least one observability component is enabled.
//
// This is useful for checking whether the observability stack should be initialized
// at all. If this returns false, the observability system can be safely bypassed.
//
// Example:
//
//	if cfg.IsEnabled() {
//	    obs, _ := observability.Init(log, nil)
//	    defer obs.Shutdown(ctx)
//	}
func (c Config) IsEnabled() bool {
	return c.MetricsEnabled || c.TracingEnabled || c.LogsEnabled || c.ProfilingEnabled
}

// EnabledComponents returns a list of names of enabled observability components.
//
// The returned slice contains one or more of: "metrics", "tracing", "logs", "profiling".
// If no components are enabled, returns an empty slice.
//
// This is useful for logging which components are active during initialization.
//
// Example:
//
//	components := cfg.EnabledComponents()
//	log.Info("Observability enabled", zap.Strings("components", components))
func (c Config) EnabledComponents() []string {
	components := []string{}
	if c.MetricsEnabled {
		components = append(components, "metrics")
	}
	if c.TracingEnabled {
		components = append(components, "tracing")
	}
	if c.LogsEnabled {
		components = append(components, "logs")
	}
	if c.ProfilingEnabled {
		components = append(components, "profiling")
	}
	return components
}

// applyDefaults fills in default values for unset configuration fields.
//
// Default values:
//   - MetricExportIntervalSec: 10 seconds
//   - MetricBatchSize: 1024
//   - TraceBatchSize: 512
//   - TraceSamplingRate: environment-based (1.0 for dev/staging, 0.1 for production)
//   - HostName: auto-detected from system or HOSTNAME environment variable
func applyDefaults(cfg Config) Config {
	if cfg.MetricExportIntervalSec == 0 {
		cfg.MetricExportIntervalSec = 10
	}
	if cfg.MetricBatchSize == 0 {
		cfg.MetricBatchSize = 1024
	}
	if cfg.TraceBatchSize == 0 {
		cfg.TraceBatchSize = 512
	}
	if cfg.TraceSamplingRate == 0 {
		cfg.TraceSamplingRate = getDefaultSamplingRate(cfg.Environment)
	}
	if cfg.HostName == "" {
		cfg.HostName = getHostName()
	}
	return cfg
}

// getDefaultSamplingRate returns the default trace sampling rate based on environment.
//
// Sampling rates:
//   - development, dev, local: 1.0 (100% - sample everything)
//   - staging, stage, test: 1.0 (100% - sample everything)
//   - production, prod: 0.1 (10% - sample 1 in 10 traces)
//   - unknown: 0.05 (5% - conservative default)
func getDefaultSamplingRate(env string) float64 {
	switch strings.ToLower(env) {
	case "development", "dev", "local":
		return 1.0
	case "staging", "stage", "test":
		return 1.0
	case "production", "prod":
		return 0.1
	default:
		return 0.05
	}
}

// getHostName returns the system hostname.
//
// It first checks the HOSTNAME environment variable, then falls back to
// os.Hostname(). If both fail, returns "unknown".
func getHostName() string {
	if hostname := os.Getenv("HOSTNAME"); hostname != "" {
		return hostname
	}
	if hostname, err := os.Hostname(); err == nil {
		return hostname
	}
	return "unknown"
}

// isValidEndpoint verifies that an endpoint string is in valid host:port format.
//
// Returns true if the endpoint contains exactly one colon, a non-empty host,
// and a valid numeric port number.
func isValidEndpoint(endpoint string) bool {
	parts := strings.Split(endpoint, ":")
	if len(parts) != 2 {
		return false
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return false
	}
	return parts[0] != ""
}

// getEnvString returns the value of an environment variable or a default value.
//
// If the environment variable is set and non-empty, returns its value.
// Otherwise returns defaultValue.
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt returns an environment variable parsed as an integer or a default value.
//
// If the environment variable is set and can be parsed as an integer, returns
// the parsed value. Otherwise returns defaultValue.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvBool returns an environment variable parsed as a boolean or a default value.
//
// Values "true" (case-insensitive) and "1" are considered true.
// All other values, including empty string, are considered false.
// If the variable is not set, returns defaultValue.
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return strings.ToLower(value) == "true" || value == "1"
	}
	return defaultValue
}

// getEnvFloat returns an environment variable parsed as a float64 or a default value.
//
// If the environment variable is set and can be parsed as a float64, returns
// the parsed value. Otherwise returns defaultValue.
func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}
