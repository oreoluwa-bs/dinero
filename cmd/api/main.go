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
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
	"github.com/oreoluwa-bs/dinero/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.NewConfig()

	logger := logger.New(cfg)
	slog.SetDefault(logger)

	db := database.NewDatabase(cfg.DATABASE_URL)
	if err := database.Up(db, "database/migrations"); err != nil {
		slog.Error("migrations failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	store := repository.New(db)
	paymentPrv := provider.NewMockProvider()
	qu, err := queue.New(cfg.RABBITMQ_URL)
	if err != nil {
		slog.Error("queue init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)

	apiServer := server.NewServer(paymentPrv, *store, db, qu, logger, reg, mtr)

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
