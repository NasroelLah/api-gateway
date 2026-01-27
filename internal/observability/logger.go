package observability

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LoggerConfig holds configuration for the logger
type LoggerConfig struct {
	Level  string // debug, info, warn, error
	Format string // json, console
	Output string // stdout, stderr, or file path
}

// Logger wraps zap.Logger with additional functionality
type Logger struct {
	*zap.Logger
	config LoggerConfig
}

// NewLogger creates a new structured logger based on configuration
// Requirements: 4.1, 4.3, 4.5
func NewLogger(cfg LoggerConfig) (*Logger, error) {
	level, err := parseLogLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	encoder := createEncoder(cfg.Format)
	writer := createWriter(cfg.Output)

	core := zapcore.NewCore(encoder, writer, level)
	zapLogger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return &Logger{
		Logger: zapLogger,
		config: cfg,
	}, nil
}

// NewDefaultLogger creates a logger with default settings (JSON format, info level, stdout)
func NewDefaultLogger() (*Logger, error) {
	return NewLogger(LoggerConfig{
		Level:  "info",
		Format: "json",
		Output: "stdout",
	})
}

// parseLogLevel converts string level to zapcore.Level
func parseLogLevel(level string) (zapcore.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("invalid log level: %s", level)
	}
}

// createEncoder creates the appropriate encoder based on format
func createEncoder(format string) zapcore.Encoder {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	if strings.ToLower(format) == "console" {
		return zapcore.NewConsoleEncoder(encoderConfig)
	}
	// Default to JSON format for log aggregation compatibility
	return zapcore.NewJSONEncoder(encoderConfig)
}

// createWriter creates the appropriate writer based on output
func createWriter(output string) zapcore.WriteSyncer {
	switch strings.ToLower(output) {
	case "stdout", "":
		return zapcore.AddSync(os.Stdout)
	case "stderr":
		return zapcore.AddSync(os.Stderr)
	default:
		// Treat as file path
		file, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			// Fallback to stdout if file cannot be opened
			return zapcore.AddSync(os.Stdout)
		}
		return zapcore.AddSync(file)
	}
}

// With creates a child logger with additional fields
func (l *Logger) With(fields ...zap.Field) *Logger {
	return &Logger{
		Logger: l.Logger.With(fields...),
		config: l.config,
	}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}

// GetZapLogger returns the underlying zap.Logger
func (l *Logger) GetZapLogger() *zap.Logger {
	return l.Logger
}
