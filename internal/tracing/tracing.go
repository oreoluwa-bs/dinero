package tracing

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

func InitTracer(ctx context.Context, serviceName, otlpEndpoint string, logger *slog.Logger) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(otlpEndpoint),
	)
	if err != nil {
		logger.Error("failed to create OTLP trace exporter", slog.String("error", err.Error()))
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			attribute.String("service.version", "1.0.0"),
		),
	)
	if err != nil {
		logger.Error("failed to create trace resource", slog.String("error", err.Error()))
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	logger.Info("tracer initialized", slog.String("service", serviceName), slog.String("endpoint", otlpEndpoint))

	return provider, nil
}

func Shutdown(ctx context.Context, provider *sdktrace.TracerProvider, logger *slog.Logger) {
	if provider == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := provider.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown tracer", slog.String("error", err.Error()))
	}
}
