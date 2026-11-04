package util

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ErrorResponse struct {
	Error string `json:"error"`
}

func WriteJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so a failed write cannot change the response.
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Warn("write json response failed", "error", err)
	}
}
