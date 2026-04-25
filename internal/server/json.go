package server

import (
	"encoding/json"
	"net/http"
)

type ApiResponse struct {
	Data    any    `json:"data,omitempty"`
	Message string `json:"message"`
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload ApiResponse) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ApiResponse{Message: message})
}
