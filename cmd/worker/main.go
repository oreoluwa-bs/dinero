package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oreoluwa-bs/dinero/database"
	"github.com/oreoluwa-bs/dinero/internal/config"
	"github.com/oreoluwa-bs/dinero/internal/logger"
	"github.com/oreoluwa-bs/dinero/internal/payment"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.NewConfig()

	lg := logger.New(cfg)
	slog.SetDefault(lg)

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
	rabbit, err := queue.New(cfg.RABBITMQ_URL)
	if err != nil {
		slog.Error("queue init failed",
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	paymentSvc := payment.NewService(*store, paymentPrv, db, lg)

	paymentSvc.StartRetryPoller(ctx, rabbit, 5*time.Second)
	paymentSvc.StartProcessingSweeper(ctx, 5*time.Second)

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
