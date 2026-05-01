package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	applog "github.com/oreoluwa-bs/dinero/internal/logger"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

func (s Server) getCharge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lg := applog.WithTrace(ctx, s.logger)
	span := trace.SpanFromContext(ctx)

	ref := chi.URLParam(r, "reference")
	if ref == "" {
		writeError(w, http.StatusBadRequest, "missing reference")
		return
	}
	span.SetAttributes(attribute.String("charge.reference", ref))

	dbCtx, dbSpan := s.tracer.Start(ctx, "db.GetPaymentByReference")
	payment, err := s.store.GetPaymentByReference(dbCtx, ref)
	dbSpan.End()
	if err != nil {
		if err == sql.ErrNoRows {
			lg.Info("payment not found", "reference", ref)
			writeError(w, http.StatusNotFound, "payment not found")
			return
		}
		lg.Error("failed to get payment", "reference", ref, "error", err.Error())
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	lg.Info("payment lookup",
		"reference", payment.Reference,
		"status", payment.Status,
		"attempts", payment.Attempts,
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
	ctx := r.Context()
	lg := applog.WithTrace(ctx, s.logger)
	span := trace.SpanFromContext(ctx)

	var req createChargeRequest
	if err := decodeJSON(r, &req); err != nil {
		lg.Error("failed to decode charge request", "error", err.Error())
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

	lg.Info("charge request received",
		"reference", req.Reference,
		"idempotency_key", idemKey,
		"amount", req.Amount,
		"currency", req.Currency,
	)

	idemCtx, idemSpan := s.tracer.Start(ctx, "db.GetPaymentByIdempotency")
	existingPayment, err := s.store.GetPaymentByIdempotency(idemCtx, sql.NullString{
		String: idemKey,
		Valid:  idemKey != "",
	})
	idemSpan.End()
	if err != nil && err != sql.ErrNoRows {
		lg.Error("failed to check idempotency",
			"idempotency_key", idemKey,
			"error", err.Error(),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err == nil {
		lg.Info("idempotent replay, returning existing payment",
			"idempotency_key", idemKey,
			"reference", existingPayment.Reference,
			"status", existingPayment.Status,
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
		lg.Error("failed to begin transaction",
			"reference", req.Reference,
			"error", err.Error(),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	qtx := s.store.WithTx(tx)

	createCtx, createSpan := s.tracer.Start(ctx, "db.CreatePayment")
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
		lg.Error("failed to create payment",
			"reference", req.Reference,
			"error", err.Error(),
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
		lg.Error("failed to marshal outbox payload",
			"reference", c.Reference,
			"error", err.Error(),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	outboxCtx, outboxSpan := s.tracer.Start(ctx, "db.InsertOutbox")
	err = qtx.InsertOutbox(outboxCtx, repository.InsertOutboxParams{
		Topic:   "payments.queue",
		Payload: payload,
	})
	outboxSpan.End()
	if err != nil {
		lg.Error("failed to insert outbox",
			"reference", c.Reference,
			"error", err.Error(),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, commitSpan := s.tracer.Start(ctx, "db.commit")
	err = tx.Commit()
	commitSpan.End()
	if err != nil {
		lg.Error("failed to commit transaction",
			"reference", c.Reference,
			"error", err.Error(),
		)
		span.RecordError(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	lg.Info("payment created and queued via outbox",
		"idempotency_key", c.IdempotencyKey.String,
		"reference", c.Reference,
		"status", c.Status,
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
	lg := applog.WithTrace(ctx, s.logger)

	if err := s.db.PingContext(ctx); err != nil {
		lg.Error("readiness check failed", "error", err.Error())
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	writeJSON(w, http.StatusOK, ApiResponse{Message: "ready"})
}
