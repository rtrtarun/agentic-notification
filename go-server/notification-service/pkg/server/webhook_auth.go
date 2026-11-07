package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/agentpulse/notification-service/pkg/util"
)

const (
	signatureHeader = "X-Hub-Signature-256"
	signaturePrefix = "sha256="
)

// webhookAuthMiddleware verifies the GitHub HMAC signature over the raw request
// body and caps the body size. It fails closed: with no secret configured the
// request is rejected unless unsigned webhooks were explicitly allowed.
func (s *Server) webhookAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := s.cfg.GitHubWebhookSecret
		if secret == "" && !s.cfg.AllowUnsignedWebhooks {
			util.WriteJSON(w, http.StatusServiceUnavailable, util.ErrorResponse{Error: "webhook secret not configured"})
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bodyLimit(s.cfg.MaxBodyBytes)))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				util.WriteJSON(w, http.StatusRequestEntityTooLarge, util.ErrorResponse{Error: "request body too large"})
				return
			}
			util.WriteJSON(w, http.StatusBadRequest, util.ErrorResponse{Error: "unreadable request body"})
			return
		}

		if secret != "" && !validSignature(secret, body, r.Header.Get(signatureHeader)) {
			s.logFor(r.Context()).Warn("rejected webhook with invalid signature", "remote", r.RemoteAddr)
			util.WriteJSON(w, http.StatusUnauthorized, util.ErrorResponse{Error: "invalid signature"})
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// validSignature compares the header against HMAC-SHA256(secret, body) in constant time.
func validSignature(secret string, body []byte, header string) bool {
	if !strings.HasPrefix(header, signaturePrefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
