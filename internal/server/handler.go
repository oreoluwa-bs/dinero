package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

func (s Server) getCharge(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "reference")
	if ref == "" {
		writeError(w, http.StatusBadRequest, "missing reference")
		return
	}

	payment, err := s.store.GetPaymentByReference(r.Context(), ref)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "payment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

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
	var req createChargeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	idemKey := r.Header.Get("idempotency_key")
	if idemKey == "" {
		idemKey = req.Reference // fallback
	}

	existingPayment, err := s.store.GetPaymentByIdempotency(ctx, sql.NullString{
		String: idemKey,
		Valid:  idemKey != "",
	})
	if err != nil && err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err == nil {
		writeJSON(w, http.StatusOK, ApiResponse{
			Data:    existingPayment,
			Message: "Created Successfully",
		})
		return
	}

	c, err := s.store.CreatePayment(context.Background(), repository.CreatePaymentParams{
		Amount:    req.Amount,
		Currency:  req.Currency,
		Reference: req.Reference,
		Status:    "pending",
		IdempotencyKey: sql.NullString{
			String: idemKey,
			Valid:  idemKey != "",
		},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(map[string]string{"payment_idempotency_key": c.IdempotencyKey.String,
		"payment_reference": c.Reference, "status": "created"})
	s.publisher.Publish(context.Background(), "", "payments.queue", payload)

	writeJSON(w, http.StatusAccepted, ApiResponse{
		Data:    c,
		Message: "Charge accepted",
	})
}
