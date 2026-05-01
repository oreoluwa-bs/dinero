package main

import (
	"context"
	"encoding/json"
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
	"github.com/oreoluwa-bs/dinero/internal/payment"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
	"github.com/oreoluwa-bs/dinero/internal/tracing"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.NewConfig()

	lg := logger.New(cfg)
	slog.SetDefault(lg)

	tracerProvider, err := tracing.InitTracer(ctx, "dinero-worker", lg)
	if err != nil {
		slog.Error("tracer init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer tracing.Shutdown(ctx, tracerProvider, lg)
	tracer := tracerProvider.Tracer("github.com/oreoluwa-bs/dinero")

	db := database.NewDatabase(cfg.DATABASE_URL)
	if err := database.Up(db, "database/migrations"); err != nil {
		slog.Error("migrations failed",
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}
	defer db.Close()

	store := repository.New(db)
	paymentPrv := provider.NewMockProvider()
	rabbit, err := queue.New(cfg.RABBITMQ_URL, tracer)
	if err != nil {
		slog.Error("queue init failed",
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	reg := metrics.NewRegistry()
	mtr := metrics.NewMetrics(reg)

	paymentSvc := payment.NewService(*store, paymentPrv, db, lg, mtr, tracer)

	paymentSvc.StartRetryPoller(ctx, rabbit, 5*time.Second)
	paymentSvc.StartProcessingSweeper(ctx, 5*time.Second)

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.HandlerFor(reg))
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
		})
		mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()

			if err := db.PingContext(ctx); err != nil {
				slog.Error("worker readiness check failed", slog.String("error", err.Error()))
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "database unavailable"})
				return
			}

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
		})
		slog.Info("worker metrics server listening", slog.String("port", "2112"))
		if err := http.ListenAndServe(":2112", mux); err != nil {
			slog.Error("metrics server error", slog.String("error", err.Error()))
		}
	}()

	err = rabbit.Start(ctx, "payments.queue", func(ctx context.Context, body []byte) error {
		return paymentSvc.HandlePaymentEvent(ctx, body)
	})

	if err != nil {
		slog.Error("consumer failed",
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	<-ctx.Done()
	slog.Info("Worker shutdown")
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
