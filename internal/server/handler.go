package server

import (
	"net/http"

	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

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

	if err := s.paymentProvider.Charge(ctx, provider.CreateCharge{
		Amount:    req.Amount,
		Currency:  req.Currency,
		Reference: req.Reference,
	}); err != nil {
		_, lerr := s.store.CreatePayment(ctx, repository.CreatePaymentParams{
			Amount:    req.Amount,
			Currency:  req.Currency,
			Reference: req.Reference,
			Status:    "failed",
		})

		if lerr != nil {
			writeError(w, http.StatusBadRequest, lerr.Error())
			return
		}

		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	c, err := s.store.CreatePayment(ctx, repository.CreatePaymentParams{
		Amount:    req.Amount,
		Currency:  req.Currency,
		Reference: req.Reference,
		Status:    "completed",
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, ApiResponse{
		Data:    c,
		Message: "Created Successfully",
	})
}
