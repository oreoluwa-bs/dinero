package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oreoluwa-bs/dinero/database"
	"github.com/oreoluwa-bs/dinero/internal/config"
	"github.com/oreoluwa-bs/dinero/internal/logger"
	"github.com/oreoluwa-bs/dinero/internal/metrics"
	"github.com/oreoluwa-bs/dinero/internal/outbox"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
	"github.com/oreoluwa-bs/dinero/internal/server"
	"github.com/oreoluwa-bs/dinero/internal/tracing"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.NewConfig()

	logger := logger.New(cfg)
	slog.SetDefault(logger)

	tracerProvider, err := tracing.InitTracer(ctx, "dinero-api", logger)
	if err != nil {
		slog.Error("tracer init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer tracing.Shutdown(ctx, tracerProvider, logger)
	tracer := tracerProvider.Tracer("github.com/oreoluwa-bs/dinero")

	db := database.NewDatabase(cfg.DATABASE_URL)
	if err := database.Up(db, "database/migrations"); err != nil {
		slog.Error("migrations failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	store := repository.New(db)
	paymentPrv := provider.NewMockProvider()
	qu, err := queue.New(cfg.RABBITMQ_URL, tracer)
	if err != nil {
		slog.Error("queue init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)

	apiServer := server.NewServer(paymentPrv, *store, db, qu, logger, reg, mtr, tracerProvider, tracer)

	outboxPoller := outbox.NewPoller(*store, qu, logger, 2*time.Second)
	outboxPoller.Start(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.PORT,
		Handler:           apiServer.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("server listening", slog.String("port", cfg.PORT))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", slog.String("error", err.Error()))
	}
}
