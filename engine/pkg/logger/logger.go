package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// contextKey is the key type for context values
type contextKey string

const (
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request_id"
	// UserIDKey is the context key for user ID
	UserIDKey contextKey = "user_id"
	// SessionIDKey is the context key for session ID
	SessionIDKey contextKey = "session_id"
)

// Logger wraps slog.Logger with context support
type Logger struct {
	*slog.Logger
}

// New creates a new logger based on configuration
func New(cfg config.LoggingConfig) (*Logger, error) {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	writer := io.Writer(os.Stdout)
	if cfg.Output == "file" && cfg.FilePath != "" {
		// O_TRUNC clears the file on startup (fresh logs each run)
		file, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
		if err != nil {
			return nil, err
		}
		writer = file
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
	}

	handler := slog.Handler(slog.NewTextHandler(writer, opts))
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(writer, opts)
	}

	logger := slog.New(handler)
	return &Logger{Logger: logger}, nil
}

// WithContext creates a new logger with context values
func (l *Logger) WithContext(ctx context.Context) *Logger {
	args := []any{}

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		args = append(args, slog.String("request_id", requestID))
	}

	if userID, ok := ctx.Value(UserIDKey).(string); ok {
		args = append(args, slog.String("user_id", userID))
	}

	if sessionID, ok := ctx.Value(SessionIDKey).(string); ok {
		args = append(args, slog.String("session_id", sessionID))
	}

	if len(args) == 0 {
		return l
	}

	return &Logger{Logger: l.With(args...)}
}

// WithFields creates a new logger with additional fields
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	attrs := make([]any, 0, len(fields))
	for k, v := range fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	return &Logger{Logger: l.With(attrs...)}
}

// DebugContext logs a debug message with context
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.WithContext(ctx).Debug(msg, args...)
}

// InfoContext logs an info message with context
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.WithContext(ctx).Info(msg, args...)
}

// WarnContext logs a warning message with context
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.WithContext(ctx).Warn(msg, args...)
}

// ErrorContext logs an error message with context
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.WithContext(ctx).Error(msg, args...)
}

// ClearLogsDir removes all contents (files and subdirectories) from the specified logs directory.
// The directory itself is preserved, only its contents are removed.
// Returns the number of items removed and any error encountered.
func ClearLogsDir(logsDir string) (int, error) {
	// Check if directory exists
	info, err := os.Stat(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, nothing to clear
			return 0, nil
		}
		return 0, fmt.Errorf("failed to stat logs directory %s: %w", logsDir, err)
	}

	if !info.IsDir() {
		return 0, fmt.Errorf("logs path %s is not a directory", logsDir)
	}

	// Read directory entries
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read logs directory %s: %w", logsDir, err)
	}

	removedCount := 0
	for _, entry := range entries {
		entryPath := filepath.Join(logsDir, entry.Name())

		if entry.IsDir() {
			// Remove directory with all contents
			if err := os.RemoveAll(entryPath); err != nil {
				slog.Warn("failed to remove log directory", "path", entryPath, "error", err)
				continue
			}
		} else {
			// Remove file
			if err := os.Remove(entryPath); err != nil {
				slog.Warn("failed to remove log file", "path", entryPath, "error", err)
				continue
			}
		}
		removedCount++
	}

	return removedCount, nil
}
