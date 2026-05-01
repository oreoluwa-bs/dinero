package logger

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

func WithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return logger
	}

	sc := span.SpanContext()
	attrs := make([]any, 0, 2)
	if sc.HasTraceID() {
		attrs = append(attrs, slog.String("trace.id", sc.TraceID().String()))
	}
	if sc.HasSpanID() {
		attrs = append(attrs, slog.String("span.id", sc.SpanID().String()))
	}
	if len(attrs) == 0 {
		return logger
	}

	return logger.With(attrs...)
}
