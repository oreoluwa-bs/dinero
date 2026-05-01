package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"go.opentelemetry.io/otel/attribute"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

func (s Server) getCharge(w http.ResponseWriter, r *http.Request) {
	tracer := s.tracer
	ctx, span := tracer.Start(r.Context(), "server.getCharge")
	defer span.End()

	ref := chi.URLParam(r, "reference")
	if ref == "" {
		writeError(w, http.StatusBadRequest, "missing reference")
		return
	}
	span.SetAttributes(attribute.String("charge.reference", ref))

	dbCtx, dbSpan := tracer.Start(ctx, "db.GetPaymentByReference")
	payment, err := s.store.GetPaymentByReference(dbCtx, ref)
	dbSpan.End()
	if err != nil {
		if err == sql.ErrNoRows {
			s.logger.Info("payment not found", slog.String("reference", ref))
			writeError(w, http.StatusNotFound, "payment not found")
			return
		}
		s.logger.Error("failed to get payment", slog.String("reference", ref), slog.String("error", err.Error()))
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logger.Info("payment lookup",
		slog.String("reference", payment.Reference),
		slog.String("status", payment.Status),
		slog.Int64("attempts", payment.Attempts),
	)

	writeJSON(w, http.StatusOK, ApiResponse{
		Data:    payment,
		Message: "Payment found",
	})
}

type createChargeRequest struct {
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference,omitempty"`
}

func (s Server) createCharge(w http.ResponseWriter, r *http.Request) {
	tracer := s.tracer
	ctx, span := tracer.Start(r.Context(), "server.createCharge")
	defer span.End()

	var req createChargeRequest
	if err := decodeJSON(r, &req); err != nil {
		s.logger.Error("failed to decode charge request", slog.String("error", err.Error()))
		span.RecordError(err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	idemKey := r.Header.Get("idempotency_key")
	if idemKey == "" {
		idemKey = req.Reference // fallback
	}

	span.SetAttributes(
		attribute.String("charge.reference", req.Reference),
		attribute.String("charge.idempotency_key", idemKey),
		attribute.Int64("charge.amount", req.Amount),
		attribute.String("charge.currency", req.Currency),
	)

	s.logger.Info("charge request received",
		slog.String("reference", req.Reference),
		slog.String("idempotency_key", idemKey),
		slog.Int64("amount", req.Amount),
		slog.String("currency", req.Currency),
	)

	idemCtx, idemSpan := tracer.Start(ctx, "db.GetPaymentByIdempotency")
	existingPayment, err := s.store.GetPaymentByIdempotency(idemCtx, sql.NullString{
		String: idemKey,
		Valid:  idemKey != "",
	})
	idemSpan.End()
	if err != nil && err != sql.ErrNoRows {
		s.logger.Error("failed to check idempotency",
			slog.String("idempotency_key", idemKey),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err == nil {
		s.logger.Info("idempotent replay, returning existing payment",
			slog.String("idempotency_key", idemKey),
			slog.String("reference", existingPayment.Reference),
			slog.String("status", existingPayment.Status),
		)
		writeJSON(w, http.StatusOK, ApiResponse{
			Data:    existingPayment,
			Message: "Created Successfully",
		})
		return
	}

	// Use outbox pattern: create payment and outbox entry in a single transaction
	tx, err := s.db.Begin()
	if err != nil {
		s.logger.Error("failed to begin transaction",
			slog.String("reference", req.Reference),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	qtx := s.store.WithTx(tx)

	createCtx, createSpan := tracer.Start(ctx, "db.CreatePayment")
	c, err := qtx.CreatePayment(createCtx, repository.CreatePaymentParams{
		Amount:    req.Amount,
		Currency:  req.Currency,
		Reference: req.Reference,
		Status:    "pending",
		IdempotencyKey: sql.NullString{
			String: idemKey,
			Valid:  idemKey != "",
		},
	})
	createSpan.End()
	if err != nil {
		s.logger.Error("failed to create payment",
			slog.String("reference", req.Reference),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(map[string]string{
		"payment_idempotency_key": c.IdempotencyKey.String,
		"payment_reference":       c.Reference,
		"status":                  "created",
	})
	if err != nil {
		s.logger.Error("failed to marshal outbox payload",
			slog.String("reference", c.Reference),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	outboxCtx, outboxSpan := tracer.Start(ctx, "db.InsertOutbox")
	err = qtx.InsertOutbox(outboxCtx, repository.InsertOutboxParams{
		Topic:   "payments.queue",
		Payload: payload,
	})
	outboxSpan.End()
	if err != nil {
		s.logger.Error("failed to insert outbox",
			slog.String("reference", c.Reference),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, commitSpan := tracer.Start(ctx, "db.commit")
	err = tx.Commit()
	commitSpan.End()
	if err != nil {
		s.logger.Error("failed to commit transaction",
			slog.String("reference", c.Reference),
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logger.Info("payment created and queued via outbox",
		slog.String("idempotency_key", c.IdempotencyKey.String),
		slog.String("reference", c.Reference),
		slog.String("status", c.Status),
	)

	if s.metrics != nil {
		s.metrics.PaymentsTotal.WithLabelValues("pending").Inc()
	}

	writeJSON(w, http.StatusAccepted, ApiResponse{
		Data:    c,
		Message: "Charge accepted",
	})
}

func (s Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ApiResponse{Message: "alive"})
}

func (s Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Error("readiness check failed", slog.String("error", err.Error()))
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	writeJSON(w, http.StatusOK, ApiResponse{Message: "ready"})
}
