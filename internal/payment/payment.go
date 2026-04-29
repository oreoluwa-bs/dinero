package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
}

type Service struct {
	store    repository.Queries
	provider provider.Provider
	db       *sql.DB
	logger   *slog.Logger
}

func NewService(
	store repository.Queries,
	provider provider.Provider,
	db *sql.DB,
	logger *slog.Logger,
) *Service {
	return &Service{
		store:    store,
		provider: provider,
		db:       db,
	}
}

func (s Service) HandlePaymentEvent(ctx context.Context, payload []byte) error {
	var val map[string]interface{}
	err := json.Unmarshal(payload, &val)
	if err != nil {
		return err
	}

	idemKey, ok := val["payment_idempotency_key"].(string)
	if !ok {
		return errors.New("missing idempotency key in payload")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	qtx := s.store.WithTx(tx)

	pm, err := qtx.GetPaymentByIdempotency(ctx, sql.NullString{
		String: idemKey,
		Valid:  idemKey != "",
	})
	if err != nil {
		return err
	}

	if pm.Status == "processing" {
		return nil
	}
	if pm.Status == "completed" {
		return nil
	}
	if pm.Status == "failed" && pm.Attempts > 3 {
		return nil
	}

	err = qtx.UpdatePaymentStatus(ctx, repository.UpdatePaymentStatusParams{
		Status:   "processing",
		Attempts: pm.Attempts + 1,
		IdempotencyKey: sql.NullString{
			String: idemKey,
			Valid:  idemKey != "",
		},
	})
	if err != nil {
		return err
	}

	err = s.provider.Charge(ctx, provider.CreateCharge{
		Amount:    pm.Amount,
		Currency:  pm.Currency,
		Reference: pm.Reference,
	})
	if err != nil {
		retyAt := time.Now().UTC().Add(time.Minute * time.Duration(pm.Attempts))
		lerr := qtx.UpdatePaymentStatus(ctx, repository.UpdatePaymentStatusParams{
			Status:   "failed",
			Attempts: pm.Attempts + 1,
			IdempotencyKey: sql.NullString{
				String: idemKey,
				Valid:  idemKey != "",
			},
			NextRetryAt: sql.NullString{
				String: retyAt.Format("2006-01-02 15:04:05"),
				Valid:  !retyAt.IsZero(),
			},
		})

		if lerr != nil {
			return lerr
		}

		tx.Commit()
		return nil
	}

	err = qtx.UpdatePaymentStatus(ctx, repository.UpdatePaymentStatusParams{
		Status:   "completed",
		Attempts: pm.Attempts + 1,
		IdempotencyKey: sql.NullString{
			String: idemKey,
			Valid:  idemKey != "",
		},
	})
	if err != nil {
		return err
	}

	tx.Commit()
	return nil
}

func (s Service) StartRetryPoller(ctx context.Context, publisher Publisher, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.retryFailedPayments(ctx, publisher)
			}
		}
	}()
}

func (s Service) retryFailedPayments(ctx context.Context, publisher Publisher) {
	payments, err := s.store.GetFailedPaymentsForRetry(ctx)
	if err != nil {
		return
	}
	for _, p := range payments {
		payload, _ := json.Marshal(map[string]string{
			"payment_idempotency_key": p.IdempotencyKey.String,
			"payment_reference":       p.Reference,
			"status":                  "created",
		})
		publisher.Publish(ctx, "", "payments.queue", payload)
	}
}
