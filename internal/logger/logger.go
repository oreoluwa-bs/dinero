package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/oreoluwa-bs/dinero/internal/config"
)

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func New(cfg *config.Config) *slog.Logger {
	level := parseLevel(cfg.LOG_LEVEL)

	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level:       level,
		AddSource:   false,
		ReplaceAttr: nil,
	}

	handler = slog.NewJSONHandler(os.Stdout, opts)

	logger := slog.New(handler)

	return logger
}

func WithTraceContext(logger *slog.Logger, traceID, spanID string) *slog.Logger {
	if traceID == "" && spanID == "" {
		return logger
	}

	return logger.With(
		slog.String("trace.id", traceID),
		slog.String("span.id", spanID),
	)
}
