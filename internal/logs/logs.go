// Package logs provides OpenTelemetry logs integration.
package logs

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap/zapcore"
)

// LogsProvider manages the OpenTelemetry logs pipeline.
type LogsProvider struct {
	provider *sdklog.LoggerProvider
	logger   log.Logger
}

// NewLogsProvider creates a new logs provider with OTLP exporter.
func NewLogsProvider(ctx context.Context, endpoint string, res *resource.Resource) (*LogsProvider, error) {
	// Create OTLP log exporter
	exporter, err := otlploggrpc.New(
		ctx,
		otlploggrpc.WithEndpoint(endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
	}

	// Create log provider
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter,
			sdklog.WithExportTimeout(10*time.Second),
			sdklog.WithExportMaxBatchSize(512),
		)),
		sdklog.WithResource(res),
	)

	// Set global log provider
	global.SetLoggerProvider(provider)

	return &LogsProvider{
		provider: provider,
		logger:   provider.Logger("gobservability"),
	}, nil
}

// Shutdown gracefully shuts down the logs provider.
func (lp *LogsProvider) Shutdown(ctx context.Context) error {
	if lp.provider != nil {
		return lp.provider.Shutdown(ctx)
	}
	return nil
}

// Logger returns the OTEL logger instance.
func (lp *LogsProvider) Logger() log.Logger {
	return lp.logger
}

// OTELCore creates a zapcore.Core that forwards logs to OpenTelemetry.
func OTELCore(level zapcore.Level) zapcore.Core {
	logProvider := global.GetLoggerProvider()
	otelLogger := logProvider.Logger("zap-otel-bridge")

	return &otelCore{
		logger: otelLogger,
		level:  level,
	}
}

// otelCore is a zapcore.Core implementation that sends logs to OpenTelemetry.
type otelCore struct {
	logger log.Logger
	level  zapcore.Level
	fields []zapcore.Field
}

func (c *otelCore) Enabled(level zapcore.Level) bool {
	return level >= c.level
}

func (c *otelCore) With(fields []zapcore.Field) zapcore.Core {
	clone := *c
	clone.fields = append(clone.fields[:len(c.fields):len(c.fields)], fields...)
	return &clone
}

func (c *otelCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *otelCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Combine core fields with entry fields
	allFields := append(c.fields, fields...)

	// Convert zap fields to OTEL log attributes
	attrs := make([]log.KeyValue, 0, len(allFields)+5)

	attrs = append(attrs,
		log.String("level", entry.Level.String()),
		log.String("logger", entry.LoggerName),
		log.String("caller", entry.Caller.TrimmedPath()),
	)

	// Convert zap fields to attributes
	enc := zapcore.NewMapObjectEncoder()
	for _, field := range allFields {
		field.AddTo(enc)
	}

	for key, value := range enc.Fields {
		attrs = append(attrs, convertToLogKeyValue(key, value))
	}

	// Create and emit log record
	ctx := context.Background()

	var record log.Record
	record.SetTimestamp(entry.Time)
	record.SetBody(log.StringValue(entry.Message))
	record.SetSeverity(convertLevel(entry.Level))
	record.AddAttributes(attrs...)

	c.logger.Emit(ctx, record)
	return nil
}

func (c *otelCore) Sync() error {
	return nil
}

// convertLevel converts zap level to OTEL severity.
func convertLevel(level zapcore.Level) log.Severity {
	switch level {
	case zapcore.DebugLevel:
		return log.SeverityDebug
	case zapcore.InfoLevel:
		return log.SeverityInfo
	case zapcore.WarnLevel:
		return log.SeverityWarn
	case zapcore.ErrorLevel:
		return log.SeverityError
	case zapcore.DPanicLevel, zapcore.PanicLevel, zapcore.FatalLevel:
		return log.SeverityFatal
	default:
		return log.SeverityInfo
	}
}

// convertToLogKeyValue converts a value to an OTEL log.KeyValue.
func convertToLogKeyValue(key string, value interface{}) log.KeyValue {
	switch v := value.(type) {
	case string:
		return log.String(key, v)
	case int:
		return log.Int(key, v)
	case int64:
		return log.Int64(key, v)
	case float64:
		return log.Float64(key, v)
	case bool:
		return log.Bool(key, v)
	default:
		return log.String(key, fmt.Sprintf("%v", v))
	}
}
