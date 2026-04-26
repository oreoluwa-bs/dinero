package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/oreoluwa-bs/dinero/database"
	"github.com/oreoluwa-bs/dinero/internal/config"
	"github.com/oreoluwa-bs/dinero/internal/payment"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.NewConfig()

	db := database.NewDatabase(cfg.DATABASE_URL)
	if err := database.Up(db, "database/migrations"); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}
	defer db.Close()

	store := repository.New(db)
	paymentPrv := provider.NewMockProvider()
	rabbit, err := queue.New(cfg.RABBITMQ_URL)
	if err != nil {
		log.Fatalf("queue init failed: %v", err)
	}

	paymentSvc := payment.NewService(*store, paymentPrv, db)

	err = rabbit.Start(ctx, "payments.queue", func(ctx context.Context, body []byte) error {
		return paymentSvc.HandlePaymentEvent(ctx, body)
	})

	if err != nil {
		log.Fatalf("Consumer failed: %v", err)
	}

	<-ctx.Done()
	log.Println("Worker shutdown")
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
