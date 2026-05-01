package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	applog "github.com/oreoluwa-bs/dinero/internal/logger"
	"github.com/oreoluwa-bs/dinero/internal/metrics"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

func (s Service) log(ctx context.Context) *slog.Logger {
	return applog.WithTrace(ctx, s.logger)
}

func (s Service) HandlePaymentEvent(ctx context.Context, payload []byte) error {
	tracer := s.tracer
	ctx, span := tracer.Start(ctx, "payment.HandlePaymentEvent")
	defer span.End()
	lg := s.log(ctx)

	start := time.Now()

	var val map[string]interface{}
	err := json.Unmarshal(payload, &val)
	if err != nil {
		lg.Error("failed to unmarshal payment event, dropping poison message", "error", err.Error())
		span.RecordError(err)
		return nil
	}

	idemKey, ok := val["payment_idempotency_key"].(string)
	if !ok {
		lg.Error("missing idempotency key in payment event payload, dropping poison message")
		return nil
	}

	ref, _ := val["payment_reference"].(string)

	span.SetAttributes(
		attribute.String("payment.idempotency_key", idemKey),
		attribute.String("payment.reference", ref),
	)

	lg.Info("payment event received",
		"idempotency_key", idemKey,
		"reference", ref,
	)

	tx, err := s.db.Begin()
	if err != nil {
		lg.Error("failed to begin transaction",
			"idempotency_key", idemKey,
			"error", err.Error(),
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
		lg.Error("failed to get payment by idempotency",
			"idempotency_key", idemKey,
			"error", err.Error(),
		)
		span.RecordError(err)
		return err
	}

	span.SetAttributes(
		attribute.String("payment.status", pm.Status),
		attribute.Int64("payment.attempts", pm.Attempts),
	)

	lg.Info("payment state resolved",
		"idempotency_key", idemKey,
		"reference", pm.Reference,
		"status", pm.Status,
		"attempts", pm.Attempts,
	)

	if pm.Status == "processing" {
		if pm.ProcessingStartedAt.Valid && !isLeaseExpired(pm.ProcessingStartedAt.String) {
			lg.Info("payment still within processing lease, skipping",
				"idempotency_key", idemKey,
				"reference", pm.Reference,
			)
			return nil
		}
		lg.Warn("processing lease expired, treating as retryable",
			"idempotency_key", idemKey,
			"reference", pm.Reference,
		)
	}

	if pm.Status == "completed" {
		lg.Info("payment already completed, skipping",
			"idempotency_key", idemKey,
			"reference", pm.Reference,
		)
		return nil
	}
	if pm.Status == "failed" && pm.Attempts >= MAX_ATTEMPTS {
		lg.Info("payment terminally failed, skipping",
			"idempotency_key", idemKey,
			"reference", pm.Reference,
			"attempts", pm.Attempts,
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
		lg.Error("failed to update payment status to processing",
			"idempotency_key", idemKey,
			"error", err.Error(),
		)
		span.RecordError(err)
		return err
	}

	lg.Info("payment transitioned to processing",
		"idempotency_key", idemKey,
		"reference", pm.Reference,
		"attempts", pm.Attempts+1,
	)

	_, commitSpan := tracer.Start(ctx, "db.commit")
	err = tx.Commit()
	commitSpan.End()
	if err != nil {
		lg.Error("failed to commit processing lease",
			"idempotency_key", idemKey,
			"error", err.Error(),
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
			lg.Error("failed to update payment status to failed",
				"idempotency_key", idemKey,
				"error", updateErr.Error(),
			)
			span.RecordError(updateErr)
			return updateErr
		}

		if pm.Attempts+1 >= MAX_ATTEMPTS {
			lg.Error("payment terminally failed after max retries",
				"idempotency_key", idemKey,
				"reference", pm.Reference,
				"attempts", pm.Attempts+1,
				"last_error", err.Error(),
			)
			if s.metrics != nil {
				s.metrics.PaymentsTotal.WithLabelValues("terminal").Inc()
			}
		} else {
			lg.Warn("provider charge failed, retry scheduled",
				"idempotency_key", idemKey,
				"reference", pm.Reference,
				"attempts", pm.Attempts+1,
				"next_retry_at", retyAt.Format(time.RFC3339),
				"error", err.Error(),
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
		lg.Error("failed to update payment status to completed",
			"idempotency_key", idemKey,
			"error", err.Error(),
		)
		span.RecordError(err)
		return err
	}

	lg.Info("payment completed",
		"idempotency_key", idemKey,
		"reference", pm.Reference,
		"attempts", pm.Attempts+1,
	)

	if s.metrics != nil {
		s.metrics.PaymentsTotal.WithLabelValues("completed").Inc()
		s.metrics.ProcessingDuration.Observe(time.Since(start).Seconds())
	}

	return nil
}

func (s Service) StartRetryPoller(ctx context.Context, publisher Publisher, interval time.Duration) {
	s.logger.Info("retry poller started", "interval", interval)
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
	lg := s.log(ctx)

	payments, err := s.store.GetFailedPaymentsForRetry(ctx)
	if err != nil {
		lg.Error("failed to fetch payments for retry", "error", err.Error())
		span.RecordError(err)
		return
	}
	if len(payments) == 0 {
		return
	}

	span.SetAttributes(attribute.Int("payment.retry_count", len(payments)))
	lg.Info("retrying failed payments", "count", len(payments))
	if s.metrics != nil {
		s.metrics.PendingRetry.Set(float64(len(payments)))
	}

	for _, p := range payments {
		payload, _ := json.Marshal(map[string]string{
			"payment_idempotency_key": p.IdempotencyKey.String,
			"payment_reference":       p.Reference,
			"status":                  "created",
		})
		lg.Info("republishing payment for retry",
			"idempotency_key", p.IdempotencyKey.String,
			"reference", p.Reference,
		)
		if s.metrics != nil {
			s.metrics.PaymentsRetried.Inc()
		}
		if err := publisher.Publish(ctx, "", "payments.queue", payload); err != nil {
			lg.Error("failed to republish payment for retry",
				"idempotency_key", p.IdempotencyKey.String,
				"reference", p.Reference,
				"error", err.Error(),
			)
		}
	}
}

func (s Service) StartProcessingSweeper(ctx context.Context, interval time.Duration) {
	s.logger.Info("processing payment sweeper started", "interval", interval)
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
	lg := s.log(ctx)

	rows, err := s.store.ResetStaleProcessingPayments(ctx)
	if err != nil {
		lg.Error("failed to reset stale processing payments", "error", err.Error())
		span.RecordError(err)
		return
	}

	if rows > 0 {
		span.SetAttributes(attribute.Int64("sweeper.rows_reset", rows))
		lg.Info("reset stale processing payments", "rows", rows)
		if s.metrics != nil {
			s.metrics.SweeperResets.Add(float64(rows))
		}
	}
}

func isLeaseExpired(ts string) bool {
	t, err := time.Parse("2006-01-02 15:04:05", ts)
	if err != nil {
		return true
	}
	return time.Since(t) > 2*time.Minute
}
