package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
)

const requestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// maxRequestIDLen bounds caller-supplied IDs so they cannot bloat logs.
const maxRequestIDLen = 64

// requestIDMiddleware tags every request with an ID, echoes it in the response
// header and stores it in the request context for logging. A caller-supplied
// X-Request-ID is reused only if it is short and made of safe characters.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// requestID returns the ID stored by requestIDMiddleware, or "" if none.
func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// logFor returns the server logger enriched with the request ID. It falls back
// to slog.Default so a zero-value Server (as in unit tests) never panics.
func (s *Server) logFor(ctx context.Context) *slog.Logger {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	if id := requestID(ctx); id != "" {
		logger = logger.With("request_id", id)
	}
	return logger
}
