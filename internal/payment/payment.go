package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"github.com/oreoluwa-bs/dinero/internal/metrics"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
}

const MAX_ATTEMPTS = 5

type Service struct {
	store    repository.Queries
	provider provider.Provider
	db       *sql.DB
	logger   *slog.Logger
	metrics  *metrics.Metrics
	tracer   trace.Tracer
}

func NewService(
	store repository.Queries,
	provider provider.Provider,
	db *sql.DB,
	logger *slog.Logger,
	mtr *metrics.Metrics,
	tracer trace.Tracer,
) *Service {
	return &Service{
		store:    store,
		provider: provider,
		db:       db,
		logger:   logger,
		metrics:  mtr,
		tracer:   tracer,
	}
}

func (s Service) HandlePaymentEvent(ctx context.Context, payload []byte) error {
	tracer := s.tracer
	ctx, span := tracer.Start(ctx, "payment.HandlePaymentEvent")
	defer span.End()

	start := time.Now()

	var val map[string]interface{}
	err := json.Unmarshal(payload, &val)
	if err != nil {
		s.logger.Error("failed to unmarshal payment event, dropping poison message", slog.String("error", err.Error()))
		span.RecordError(err)
		return nil // Ack — don't requeue unrecoverable errors
	}

	idemKey, ok := val["payment_idempotency_key"].(string)
	if !ok {
		s.logger.Error("missing idempotency key in payment event payload, dropping poison message")
		return nil // Ack — don't requeue unrecoverable errors
	}

	ref, _ := val["payment_reference"].(string)

	span.SetAttributes(
		attribute.String("payment.idempotency_key", idemKey),
		attribute.String("payment.reference", ref),
	)

	s.logger.Info("payment event received",
		slog.String("idempotency_key", idemKey),
		slog.String("reference", ref),
	)

	tx, err := s.db.Begin()
	if err != nil {
		s.logger.Error("failed to begin transaction",
			slog.String("idempotency_key", idemKey),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		return err
	}
	defer tx.Rollback()
	qtx := s.store.WithTx(tx)

	dbCtx, dbSpan := tracer.Start(ctx, "db.GetPaymentByIdempotency")
	pm, err := qtx.GetPaymentByIdempotency(dbCtx, sql.NullString{
		String: idemKey,
		Valid:  idemKey != "",
	})
	dbSpan.End()
	if err != nil {
		s.logger.Error("failed to get payment by idempotency",
			slog.String("idempotency_key", idemKey),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		return err
	}

	span.SetAttributes(
		attribute.String("payment.status", pm.Status),
		attribute.Int64("payment.attempts", pm.Attempts),
	)

	s.logger.Info("payment state resolved",
		slog.String("idempotency_key", idemKey),
		slog.String("reference", pm.Reference),
		slog.String("status", pm.Status),
		slog.Int64("attempts", pm.Attempts),
	)

	if pm.Status == "processing" {
		if pm.ProcessingStartedAt.Valid && !isLeaseExpired(pm.ProcessingStartedAt.String) {
			s.logger.Info("payment still within processing lease, skipping",
				slog.String("idempotency_key", idemKey),
				slog.String("reference", pm.Reference),
			)
			return nil
		}
		s.logger.Warn("processing lease expired, treating as retryable",
			slog.String("idempotency_key", idemKey),
			slog.String("reference", pm.Reference),
		)
	}

	if pm.Status == "completed" {
		s.logger.Info("payment already completed, skipping",
			slog.String("idempotency_key", idemKey),
			slog.String("reference", pm.Reference),
		)
		return nil
	}
	if pm.Status == "failed" && pm.Attempts >= MAX_ATTEMPTS {
		s.logger.Info("payment terminally failed, skipping",
			slog.String("idempotency_key", idemKey),
			slog.String("reference", pm.Reference),
			slog.Int64("attempts", pm.Attempts),
		)
		return nil
	}

	updateCtx, updateSpan := tracer.Start(ctx, "db.UpdatePaymentStatus")
	updateSpan.SetAttributes(attribute.String("payment.status", "processing"))
	err = qtx.UpdatePaymentStatus(updateCtx, repository.UpdatePaymentStatusParams{
		Status:   "processing",
		Attempts: pm.Attempts + 1,
		ProcessingStartedAt: sql.NullString{
			String: time.Now().UTC().Format("2006-01-02 15:04:05"),
			Valid:  true,
		},
		IdempotencyKey: sql.NullString{
			String: idemKey,
			Valid:  idemKey != "",
		},
	})
	updateSpan.End()
	if err != nil {
		s.logger.Error("failed to update payment status to processing",
			slog.String("idempotency_key", idemKey),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		return err
	}

	s.logger.Info("payment transitioned to processing",
		slog.String("idempotency_key", idemKey),
		slog.String("reference", pm.Reference),
		slog.Int64("attempts", pm.Attempts+1),
	)

	// This prevents double-charge because the provider call is not inside a rollbackable transaction.
	_, commitSpan := tracer.Start(ctx, "db.commit")
	err = tx.Commit()
	commitSpan.End()
	if err != nil {
		s.logger.Error("failed to commit processing lease",
			slog.String("idempotency_key", idemKey),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		return err
	}

	if s.metrics != nil {
		s.metrics.ActiveProcessing.Inc()
	}

	providerCtx, providerSpan := tracer.Start(ctx, "provider.Charge")
	providerSpan.SetAttributes(
		attribute.String("provider.reference", pm.Reference),
		attribute.Int64("provider.amount", pm.Amount),
		attribute.String("provider.currency", pm.Currency),
	)
	providerStart := time.Now()
	err = s.provider.Charge(providerCtx, provider.CreateCharge{
		Amount:    pm.Amount,
		Currency:  pm.Currency,
		Reference: pm.Reference,
	})
	if err != nil {
		providerSpan.RecordError(err)
	}
	providerSpan.End()
	if s.metrics != nil {
		s.metrics.ProviderDuration.Observe(time.Since(providerStart).Seconds())
	}

	if err != nil {
		if s.metrics != nil {
			s.metrics.ProviderCalls.WithLabelValues("error").Inc()
			s.metrics.ActiveProcessing.Dec()
		}

		retyAt := time.Now().UTC().Add(time.Minute * time.Duration(pm.Attempts))
		failCtx, failSpan := tracer.Start(ctx, "db.UpdatePaymentStatus")
		failSpan.SetAttributes(attribute.String("payment.status", "failed"))
		updateErr := s.store.UpdatePaymentStatus(failCtx, repository.UpdatePaymentStatusParams{
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
			ProcessingStartedAt: sql.NullString{},
		})
		failSpan.End()

		if updateErr != nil {
			s.logger.Error("failed to update payment status to failed",
				slog.String("idempotency_key", idemKey),
				slog.String("error", updateErr.Error()),
			)
			span.RecordError(updateErr)
			return updateErr
		}

		if pm.Attempts+1 >= MAX_ATTEMPTS {
			s.logger.Error("payment terminally failed after max retries",
				slog.String("idempotency_key", idemKey),
				slog.String("reference", pm.Reference),
				slog.Int64("attempts", pm.Attempts+1),
				slog.String("last_error", err.Error()),
			)
			if s.metrics != nil {
				s.metrics.PaymentsTotal.WithLabelValues("terminal").Inc()
			}
		} else {
			s.logger.Warn("provider charge failed, retry scheduled",
				slog.String("idempotency_key", idemKey),
				slog.String("reference", pm.Reference),
				slog.Int64("attempts", pm.Attempts+1),
				slog.String("next_retry_at", retyAt.Format(time.RFC3339)),
				slog.String("error", err.Error()),
			)
			if s.metrics != nil {
				s.metrics.PaymentsTotal.WithLabelValues("failed").Inc()
			}
		}

		if s.metrics != nil {
			s.metrics.ProcessingDuration.Observe(time.Since(start).Seconds())
		}
		return nil
	}

	if s.metrics != nil {
		s.metrics.ProviderCalls.WithLabelValues("success").Inc()
		s.metrics.ActiveProcessing.Dec()
	}

	completeCtx, completeSpan := tracer.Start(ctx, "db.UpdatePaymentStatus")
	completeSpan.SetAttributes(attribute.String("payment.status", "completed"))
	err = s.store.UpdatePaymentStatus(completeCtx, repository.UpdatePaymentStatusParams{
		Status:              "completed",
		Attempts:            pm.Attempts + 1,
		ProcessingStartedAt: sql.NullString{},
		IdempotencyKey: sql.NullString{
			String: idemKey,
			Valid:  idemKey != "",
		},
	})
	completeSpan.End()
	if err != nil {
		s.logger.Error("failed to update payment status to completed",
			slog.String("idempotency_key", idemKey),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		return err
	}

	s.logger.Info("payment completed",
		slog.String("idempotency_key", idemKey),
		slog.String("reference", pm.Reference),
		slog.Int64("attempts", pm.Attempts+1),
	)

	if s.metrics != nil {
		s.metrics.PaymentsTotal.WithLabelValues("completed").Inc()
		s.metrics.ProcessingDuration.Observe(time.Since(start).Seconds())
	}

	return nil
}

func (s Service) StartRetryPoller(ctx context.Context, publisher Publisher, interval time.Duration) {
	s.logger.Info("retry poller started", slog.Duration("interval", interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.logger.Info("retry poller shutting down")
				return
			case <-ticker.C:
				s.retryFailedPayments(ctx, publisher)
			}
		}
	}()
}

func (s Service) retryFailedPayments(ctx context.Context, publisher Publisher) {
	tracer := s.tracer
	ctx, span := tracer.Start(ctx, "payment.retryFailedPayments")
	defer span.End()

	payments, err := s.store.GetFailedPaymentsForRetry(ctx)
	if err != nil {
		s.logger.Error("failed to fetch payments for retry", slog.String("error", err.Error()))
		span.RecordError(err)
		return
	}
	if len(payments) == 0 {
		return
	}

	span.SetAttributes(attribute.Int("payment.retry_count", len(payments)))
	s.logger.Info("retrying failed payments", slog.Int("count", len(payments)))
	if s.metrics != nil {
		s.metrics.PendingRetry.Set(float64(len(payments)))
	}

	for _, p := range payments {
		payload, _ := json.Marshal(map[string]string{
			"payment_idempotency_key": p.IdempotencyKey.String,
			"payment_reference":       p.Reference,
			"status":                  "created",
		})
		s.logger.Info("republishing payment for retry",
			slog.String("idempotency_key", p.IdempotencyKey.String),
			slog.String("reference", p.Reference),
		)
		if s.metrics != nil {
			s.metrics.PaymentsRetried.Inc()
		}
		if err := publisher.Publish(ctx, "", "payments.queue", payload); err != nil {
			s.logger.Error("failed to republish payment for retry",
				slog.String("idempotency_key", p.IdempotencyKey.String),
				slog.String("reference", p.Reference),
				slog.String("error", err.Error()),
			)
		}
	}
}

func (s Service) StartProcessingSweeper(ctx context.Context, interval time.Duration) {
	// ticker goroutine, runs ResetStaleProcessingPayments
	// logs how many rows were reset
	s.logger.Info("processing payment sweeper started", slog.Duration("interval", interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.logger.Info("processing payment sweeper shutting down")
				return
			case <-ticker.C:
				s.resetStaleProcessingPayments(ctx)
			}
		}
	}()
}

func (s Service) resetStaleProcessingPayments(ctx context.Context) {
	tracer := s.tracer
	ctx, span := tracer.Start(ctx, "payment.resetStaleProcessingPayments")
	defer span.End()

	rows, err := s.store.ResetStaleProcessingPayments(ctx)
	if err != nil {
		s.logger.Error("failed to reset stale processing payments", slog.String("error", err.Error()))
		span.RecordError(err)
		return
	}

	if rows > 0 {
		span.SetAttributes(attribute.Int64("sweeper.rows_reset", rows))
		s.logger.Info("reset stale processing payments",
			slog.Int64("rows", rows))
		if s.metrics != nil {
			s.metrics.SweeperResets.Add(float64(rows))
		}
	}
}

func isLeaseExpired(ts string) bool {
	t, err := time.Parse("2006-01-02 15:04:05", ts)
	if err != nil {
		return true // malformed = expired
	}
	return time.Since(t) > 2*time.Minute
}
